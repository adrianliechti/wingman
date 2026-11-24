package server_test

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth/static"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server"

	"github.com/joho/godotenv"
)

const (
	replayKey  = "sdk-key"
	replayCode = "KIWI-2291"
	// Every backend reasons before it can choose the city.
	replayPrompt = "Find the smallest positive integer x such that x mod 7 = 3, x mod 11 = 5 and x mod 13 = 7. " +
		"The city is determined by x mod 3: 0 means Tokyo, 1 means Bern, and 2 means Paris. " +
		"Calculate x first, then call get_weather exactly once for the resulting city. Report x, the weather and the verification code."
)

// liveBackend is a real provider. Each test builds its own completer so it
// can record that completer's upstream requests.
type liveBackend struct {
	name string
	new  func(*http.Client) (provider.Completer, error)
}

// liveBackends skips unless WINGMAN_PROVIDER_LIVE=1. WINGMAN_LIVE_OPENAI_MODEL,
// WINGMAN_LIVE_ANTHROPIC_MODEL and WINGMAN_LIVE_GEMINI_MODEL override the models.
func liveBackends(t *testing.T) []liveBackend {
	if os.Getenv("WINGMAN_PROVIDER_LIVE") != "1" {
		t.Skip("set WINGMAN_PROVIDER_LIVE=1 to call real providers through each SDK surface")
	}
	values, _ := godotenv.Read("../.env")
	key := func(name string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		if values[name] == "" {
			t.Fatalf("%s is required", name)
		}
		return values[name]
	}
	model := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}
	return []liveBackend{
		{"openai", func(c *http.Client) (provider.Completer, error) {
			return openai.NewResponder("https://api.openai.com/v1", model("WINGMAN_LIVE_OPENAI_MODEL", "gpt-6.1-sol"), openai.WithToken(key("OPENAI_API_KEY")), openai.WithClient(c))
		}},
		{"anthropic", func(c *http.Client) (provider.Completer, error) {
			return anthropic.NewCompleter("https://api.anthropic.com", model("WINGMAN_LIVE_ANTHROPIC_MODEL", "claude-sonnet-5-5"), anthropic.WithToken(key("ANTHROPIC_API_KEY")), anthropic.WithClient(c))
		}},
		{"gemini", func(c *http.Client) (provider.Completer, error) {
			return google.NewCompleter(model("WINGMAN_LIVE_GEMINI_MODEL", "gemini-4-hydrogen"), google.WithToken(key("GEMINI_API_KEY")), google.WithClient(c))
		}},
	}
}

// liveServer serves one backend, wired as in production, under its name.
// Clients authenticate with replayKey in each SDK's own header.
func liveServer(t *testing.T, backend liveBackend, transport http.RoundTripper) string {
	t.Helper()
	completer, err := backend.new(&http.Client{Transport: transport, Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := static.New(replayKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.Authorizers = append(cfg.Authorizers, authorizer)
	cfg.RegisterCompleter(backend.name, signatures.ScopedTo(backend.name, completer))
	handler, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(handler)
	t.Cleanup(local.Close)
	return local.URL
}

// Paid opt-in test. Each SDK surface streams a tool turn from each real
// backend and replays it as that SDK's client does: Responses from
// output_item.done items, Messages from the streamed blocks, and
// google-genai chat with one model content per chunk. The backend's request
// for the second turn must still carry the reasoning state of the first.
//
//	WINGMAN_PROVIDER_LIVE=1 go test ./server -run TestSDKReasoningReplayLive -v -count=1
func TestSDKReasoningReplayLive(t *testing.T) {
	// Whether a backend request replays that backend's native reasoning.
	replayed := map[string]func(map[string]any) bool{
		"openai": func(body map[string]any) bool {
			return containsObject(body["input"], func(item map[string]any) bool {
				id, _ := item["id"].(string)
				return item["type"] == "reasoning" && strings.HasPrefix(id, "rs_") && item["encrypted_content"] != nil && item["encrypted_content"] != ""
			})
		},
		"anthropic": func(body map[string]any) bool {
			return containsObject(body["messages"], func(block map[string]any) bool {
				return block["type"] == "thinking" && block["signature"] != nil && block["signature"] != "" ||
					block["type"] == "redacted_thinking" && block["data"] != nil && block["data"] != ""
			})
		},
		"gemini": func(body map[string]any) bool {
			// Placeholder signatures only satisfy the validator for imported turns.
			return containsObject(body, func(step map[string]any) bool {
				signature, _ := step["signature"].(string)
				return step["type"] == "thought" && signature != "" && signature != "skip_thought_signature_validator"
			})
		},
	}
	surfaces := []struct {
		name string
		run  func(t *testing.T, base, model string)
	}{
		{"responses", replayResponses},
		{"messages", replayMessages},
		{"gemini", replayGemini},
	}

	for _, backend := range liveBackends(t) {
		for _, surface := range surfaces {
			t.Run(surface.name+"/"+backend.name, func(t *testing.T) {
				t.Parallel()
				recorder := &upstreamRecorder{}
				surface.run(t, liveServer(t, backend, recorder), backend.name)

				// The adapter retries without signatures when a backend rejects them.
				// The last request is the replay the model actually answered.
				bodies := recorder.bodies()
				if len(bodies) < 2 {
					t.Fatalf("expected a first turn and a replay upstream, got %d requests", len(bodies))
				}
				if !replayed[backend.name](bodies[len(bodies)-1]) {
					t.Fatalf("the replay sent to %s lost the first turn's reasoning: %s", backend.name, mustJSON(bodies[len(bodies)-1]))
				}
			})
		}
	}
}

// openai-python: client.responses.stream with store=False, no reasoning
// options and no include, as recommended for new stateless clients.
func replayResponses(t *testing.T, base, model string) {
	tools := []any{map[string]any{"type": "function", "name": "get_weather", "description": "Get the weather for a city",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []any{"city"}}}}
	input := []any{map[string]any{"role": "user", "content": replayPrompt}}
	events := postEvents(t, base+"/v1/responses", http.Header{"Authorization": {"Bearer " + replayKey}},
		map[string]any{"model": model, "input": input, "tools": tools, "store": false, "stream": true})

	var items []any
	var call map[string]any
	for _, event := range events {
		switch event["type"] {
		case "error", "response.failed":
			t.Fatalf("first turn failed: %s", mustJSON(event))
		case "response.output_item.done":
			item := event["item"].(map[string]any)
			items = append(items, item)
			switch item["type"] {
			case "function_call":
				call = item
			case "reasoning":
				// Plain requests carry the replay token, never reasoning text.
				if len(item["summary"].([]any)) != 0 || len(item["content"].([]any)) != 0 {
					t.Fatalf("plain request exposed reasoning text: %s", mustJSON(item))
				}
				if ciphertext, _ := item["encrypted_content"].(string); ciphertext == "" {
					t.Fatalf("reasoning item has no encrypted_content to replay: %s", mustJSON(item))
				}
			}
		}
	}
	if call == nil {
		t.Fatalf("no function call in %s", mustJSON(items))
	}
	input = append(input, items...)
	input = append(input, map[string]any{"type": "function_call_output", "call_id": call["call_id"], "output": weatherResult(call["arguments"])})
	answer := postJSON(t, base+"/v1/responses", http.Header{"Authorization": {"Bearer " + replayKey}},
		map[string]any{"model": model, "input": input, "tools": tools, "store": false})
	if answer["status"] != "completed" || !strings.Contains(mustJSON(answer["output"]), replayCode) {
		t.Fatalf("replayed turn did not use the tool result: %s", mustJSON(answer))
	}
}

// anthropic-python: client.messages.stream, then the final message's blocks
// replayed with the tool result.
func replayMessages(t *testing.T, base, model string) {
	header := http.Header{"x-api-key": {replayKey}, "anthropic-version": {"2023-06-01"}}
	tools := []any{map[string]any{"name": "get_weather", "description": "Get the weather for a city",
		"input_schema": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []any{"city"}}}}
	messages := []any{map[string]any{"role": "user", "content": replayPrompt}}
	events := postEvents(t, base+"/v1/messages", header,
		map[string]any{"model": model, "max_tokens": 8000, "messages": messages, "tools": tools, "stream": true})

	var blocks []map[string]any
	var partial []string
	for _, event := range events {
		switch event["type"] {
		case "error":
			t.Fatalf("first turn failed: %s", mustJSON(event))
		case "content_block_start":
			blocks = append(blocks, event["content_block"].(map[string]any))
			partial = append(partial, "")
		case "content_block_delta":
			index := int(event["index"].(float64))
			block, delta := blocks[index], event["delta"].(map[string]any)
			switch delta["type"] {
			case "text_delta":
				block["text"] = block["text"].(string) + delta["text"].(string)
			case "thinking_delta":
				block["thinking"] = block["thinking"].(string) + delta["thinking"].(string)
			case "signature_delta":
				block["signature"] = delta["signature"]
			case "input_json_delta":
				partial[index] += delta["partial_json"].(string)
			}
		case "content_block_stop":
			index := int(event["index"].(float64))
			if blocks[index]["type"] == "tool_use" && partial[index] != "" {
				var input map[string]any
				if err := json.Unmarshal([]byte(partial[index]), &input); err != nil {
					t.Fatalf("tool input %q: %v", partial[index], err)
				}
				blocks[index]["input"] = input
			}
		}
	}
	var call map[string]any
	for _, block := range blocks {
		if block["type"] == "tool_use" {
			call = block
		}
	}
	if call == nil {
		t.Fatalf("no tool_use block in %s", mustJSON(blocks))
	}
	messages = append(messages, map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": call["id"], "content": weatherResult(call["input"])},
	}})
	answer := postJSON(t, base+"/v1/messages", header,
		map[string]any{"model": model, "max_tokens": 8000, "messages": messages, "tools": tools})
	if answer["stop_reason"] != "end_turn" || !strings.Contains(mustJSON(answer["content"]), replayCode) {
		t.Fatalf("replayed turn did not use the tool result: %s", mustJSON(answer))
	}
}

// python-genai: a chat with a types.Schema declaration, which serializes
// OpenAPI types in upper case. The chat records each streamed candidate
// content as its own model turn and sends bytes back URL-safe encoded.
func replayGemini(t *testing.T, base, model string) {
	url := base + "/v1beta/models/" + model + ":streamGenerateContent?alt=sse"
	header := http.Header{"x-goog-api-key": {replayKey}}
	tools := []any{map[string]any{"functionDeclarations": []any{map[string]any{
		"name": "get_weather", "description": "Get the weather for a city",
		"parameters": map[string]any{"type": "OBJECT", "properties": map[string]any{"city": map[string]any{"type": "STRING"}}, "required": []any{"city"}},
	}}}}
	history := []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": replayPrompt}}}}
	turn := func() []map[string]any {
		t.Helper()
		var parts []map[string]any
		for i, chunk := range postEvents(t, url, header, map[string]any{"contents": history, "tools": tools}) {
			if chunk["error"] != nil {
				t.Fatalf("stream failed: %s", mustJSON(chunk))
			}
			candidates, _ := chunk["candidates"].([]any)
			if len(candidates) == 0 {
				t.Fatalf("chunk %d has no candidate, so python-genai discards the turn: %s", i, mustJSON(chunk))
			}
			content, _ := candidates[0].(map[string]any)["content"].(map[string]any)
			chunkParts, _ := content["parts"].([]any)
			if content["role"] != "model" || len(chunkParts) == 0 {
				t.Fatalf("chunk %d has no model content, so python-genai discards the turn: %s", i, mustJSON(chunk))
			}
			for _, raw := range chunkParts {
				part := raw.(map[string]any)
				if len(part) == 0 {
					t.Fatalf("chunk %d has an empty part", i)
				}
				if encoded, ok := part["thoughtSignature"].(string); ok {
					signature, err := base64.StdEncoding.DecodeString(encoded)
					if err != nil {
						t.Fatalf("thoughtSignature %q: %v", encoded, err)
					}
					part["thoughtSignature"] = base64.URLEncoding.EncodeToString(signature)
				}
				parts = append(parts, part)
			}
			history = append(history, content)
		}
		return parts
	}

	var call map[string]any
	for _, part := range turn() {
		if fc, ok := part["functionCall"].(map[string]any); ok {
			call = fc
		}
	}
	if call == nil {
		t.Fatal("no function call streamed")
	}
	response := map[string]any{"name": call["name"], "response": map[string]any{"result": weatherResult(call["args"])}}
	if id := call["id"]; id != nil {
		response["id"] = id
	}
	history = append(history, map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": response}}})
	var answer strings.Builder
	for _, part := range turn() {
		if text, _ := part["text"].(string); part["thought"] != true {
			answer.WriteString(text)
		}
	}
	if !strings.Contains(answer.String(), replayCode) {
		t.Fatalf("replayed turn did not use the tool result: %q", answer.String())
	}
}

func weatherResult(arguments any) string {
	city := ""
	switch args := arguments.(type) {
	case string:
		var decoded map[string]any
		_ = json.Unmarshal([]byte(args), &decoded)
		city, _ = decoded["city"].(string)
	case map[string]any:
		city, _ = args["city"].(string)
	}
	return fmt.Sprintf(`{"city":%q,"forecast":"sunny, 21C","verification_code":%q}`, city, replayCode)
}

type upstreamRecorder struct {
	mu       sync.Mutex
	requests []map[string]any
}

func (r *upstreamRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		data, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(data))
		var body map[string]any
		if json.Unmarshal(data, &body) == nil {
			r.mu.Lock()
			r.requests = append(r.requests, body)
			r.mu.Unlock()
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

func (r *upstreamRecorder) bodies() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.requests...)
}

func containsObject(v any, match func(map[string]any) bool) bool {
	switch v := v.(type) {
	case map[string]any:
		if match(v) {
			return true
		}
		for _, child := range v {
			if containsObject(child, match) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsObject(child, match) {
				return true
			}
		}
	}
	return false
}

func send(t *testing.T, url string, header http.Header, body map[string]any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: HTTP %d: %s", url, resp.StatusCode, raw)
	}
	return resp
}

func postJSON(t *testing.T, url string, header http.Header, body map[string]any) map[string]any {
	t.Helper()
	resp := send(t, url, header, body)
	defer resp.Body.Close()
	var result map[string]any
	if err := json.UnmarshalRead(resp.Body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func postEvents(t *testing.T, url string, header http.Header, body map[string]any) []map[string]any {
	t.Helper()
	resp := send(t, url, header, body)
	defer resp.Body.Close()
	var events []map[string]any
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("event %q: %v", data, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("%s returned no events", url)
	}
	return events
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
