package protocol_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"

	"github.com/joho/godotenv"
)

type nativeCapture struct {
	mu        sync.Mutex
	responses []capturedResponse
}

type capturedResponse struct {
	status      int
	contentType string
	body        []byte
	requestBody []byte
	requestID   string
}

func (c *nativeCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	var requestBody []byte
	if req.Body != nil {
		var err error
		requestBody, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(requestBody))
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	// Buffer this controlled live comparison so the assertions see the same
	// native generation as the handler. Separate tests cover stream latency.
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	c.mu.Lock()
	c.responses = append(c.responses, capturedResponse{
		status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: raw,
		requestBody: requestBody, requestID: resp.Header.Get("Request-Id"),
	})
	c.mu.Unlock()
	return resp, nil
}

func (c *nativeCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = nil
}

func (c *nativeCapture) only(t *testing.T) capturedResponse {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.responses) != 1 {
		t.Fatalf("native API calls = %d, want 1", len(c.responses))
	}
	return c.responses[0]
}

func referenceLive(t *testing.T, keys ...string) {
	t.Helper()
	if os.Getenv("PROTOCOL_REFERENCE_LIVE") != "1" {
		t.Skip("set PROTOCOL_REFERENCE_LIVE=1 for paid native API comparisons")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			if err := godotenv.Load(filepath.Join(dir, ".env")); err != nil {
				t.Fatal(err)
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if len(keys) == 0 {
		keys = []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY"}
	}
	for _, key := range keys {
		if os.Getenv(key) == "" {
			t.Fatalf("%s is required for the requested live matrix", key)
		}
	}
}

type liveBackend struct {
	name, protocol, model string
	capture               *nativeCapture
}

const (
	liveOpenAIModel    = "gpt-6.1-sol"
	liveAnthropicModel = "claude-sonnet-5-5"
	liveGeminiModel    = "gemini-3.8-flash"
)

func referenceBackends(t *testing.T) (*config.Config, []liveBackend) {
	t.Helper()
	cfg := &config.Config{Policy: noop.New()}
	backends := []liveBackend{
		{name: "openai-responses", protocol: "responses", model: liveOpenAIModel},
		{name: "openai-chat", protocol: "chat", model: liveOpenAIModel},
		{name: "anthropic", protocol: "anthropic", model: liveAnthropicModel},
		{name: "gemini", protocol: "interactions", model: liveGeminiModel},
	}
	for i := range backends {
		b := &backends[i]
		b.capture = &nativeCapture{}
		client := &http.Client{Transport: b.capture, Timeout: 120 * time.Second}
		var backend provider.Completer
		var err error
		switch b.name {
		case "openai-responses":
			backend, err = openai.NewResponder("https://api.openai.com/v1", b.model, openai.WithClient(client), openai.WithToken(os.Getenv("OPENAI_API_KEY")), openai.WithMaxRetries(0))
		case "openai-chat":
			backend, err = openai.NewCompleter("https://api.openai.com/v1", b.model, openai.WithClient(client), openai.WithToken(os.Getenv("OPENAI_API_KEY")), openai.WithMaxRetries(0))
		case "anthropic":
			backend, err = anthropic.NewCompleter("https://api.anthropic.com", b.model, anthropic.WithClient(client), anthropic.WithToken(os.Getenv("ANTHROPIC_API_KEY")), anthropic.WithMaxRetries(0))
		case "gemini":
			backend, err = google.NewCompleter(b.model, google.WithClient(client), google.WithToken(os.Getenv("GEMINI_API_KEY")))
		}
		if err != nil {
			t.Fatal(err)
		}
		cfg.RegisterCompleter(b.name, signatures.ScopedTo(b.model, backend))
	}
	return cfg, backends
}

func TestLiveNativeResponseParity(t *testing.T) {
	referenceLive(t)
	cfg, backends := referenceBackends(t)
	local := configuredGateway(t, cfg)
	local.Client().Timeout = 120 * time.Second
	for _, backend := range backends {
		t.Logf("live backend %s: %s", backend.name, backend.model)
		for _, stream := range []bool{false, true} {
			for _, p := range protocols(stream) {
				for _, scenario := range []string{"text", "tool"} {
					name := backend.name + "/" + p.name + "/" + scenario
					if backend.name == "openai-chat" && scenario == "tool" {
						name += "_unsupported"
					}
					if stream {
						name += "/stream"
					} else {
						name += "/json"
					}
					t.Run(name, func(t *testing.T) {
						p := liveRequest(t, p, backend.name, scenario)
						backend.capture.reset()
						status, headers, raw := post(t, local, p.path, p.body)
						native := backend.capture.only(t)
						saveReferenceFields(t, map[string]any{"request": p.body, "upstream_request": jsontext.Value(native.requestBody), "native": string(native.body), "wingman": string(raw)})
						if backend.name == "openai-chat" && scenario == "tool" {
							// GPT-6.1 Sol requires reasoning; native Chat Completions
							// rejects tools with reasoning. Test its error mapping,
							// while Responses above exercises successful tool calls.
							compareUnsupportedChatTool(t, p.name, native, status, headers, raw)
							return
						}
						requireStatus(t, status, 200, raw)
						if stream {
							requireType(t, headers, "text/event-stream")
						} else {
							requireType(t, headers, "application/json")
						}
						requireStatus(t, native.status, 200, native.body)
						want := readReference(t, backend.protocol, strings.Contains(native.contentType, "text/event-stream"), native.body)
						got := readReference(t, p.name, stream, raw)
						if !want.done || !got.done {
							t.Fatal("missing successful terminal state")
						}
						if scenario == "text" && strings.TrimSpace(want.text) == "" {
							t.Fatal("reference returned no text")
						}
						if scenario == "tool" && len(want.calls) != 1 {
							t.Fatalf("reference returned %d tools, want 1", len(want.calls))
						}
						equal(t, "text from same generation", got.text, want.text)
						equal(t, "normalized stop reason", got.finish, want.finish)
						if !reflect.DeepEqual(got.calls, want.calls) {
							t.Fatalf("tool payload changed: got %+v, native %+v", got.calls, want.calls)
						}
						if want.input <= 0 || want.output <= 0 {
							t.Fatalf("missing native usage: %+v", want)
						}
						equal(t, "input tokens", got.input, want.input)
						equal(t, "output tokens including reasoning", got.output, want.output)
						equal(t, "cache read tokens", got.cached, want.cached)
						if want.reasoningKnown {
							if want.reasoning > 0 && !got.reasoningKnown {
								t.Error("reported reasoning-token breakdown was lost")
							}
							equal(t, "reasoning tokens", got.reasoning, want.reasoning)
						}
					})
				}
			}
		}
	}
}

func liveRequest(t *testing.T, p protocol, model, scenario string) protocol {
	t.Helper()
	// Each scenario owns a request; the base matrix shares maps within a row.
	raw, err := json.Marshal(p.body)
	if err != nil {
		t.Fatal(err)
	}
	p.body = exactObject(t, raw)
	if p.name != "gemini" {
		p.body["model"] = model
	}
	if p.body["stream"] == false {
		delete(p.body, "stream_options")
	}
	p.path = strings.ReplaceAll(p.path, "contract-model", model)
	prompt := "Reply with exactly: Gruezi reference 42"
	if scenario == "tool" {
		prompt = `Call record exactly once with city "Zürich" and value 9007199254740993. Do not answer in text.`
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}, "value": map[string]any{"type": "integer"}}, "required": []string{"city", "value"}, "additionalProperties": false}
	switch p.name {
	case "chat", "anthropic":
		p.body["messages"] = []any{map[string]any{"role": "user", "content": prompt}}
		if p.name == "anthropic" {
			p.body["max_tokens"] = 2048
		}
	case "responses":
		p.body["input"] = prompt
	case "gemini":
		p.body["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}}
	}
	if scenario == "tool" {
		switch p.name {
		case "chat":
			p.body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "record", "parameters": schema}}}
			p.body["tool_choice"] = "required"
		case "responses":
			p.body["tools"] = []any{map[string]any{"type": "function", "name": "record", "parameters": schema}}
			p.body["tool_choice"] = "required"
		case "anthropic":
			p.body["tools"] = []any{map[string]any{"name": "record", "input_schema": schema}}
			p.body["tool_choice"] = map[string]any{"type": "any"}
		case "gemini":
			p.body["tools"] = []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": "record", "parameters": schema}}}}
			p.body["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}}
		}
		// Claude 5.5 rejects forced tool choice. Exercise automatic selection
		// with the same explicit tool prompt and require exactly one native call.
		if model == "anthropic" {
			switch p.name {
			case "chat", "responses":
				p.body["tool_choice"] = "auto"
			case "anthropic":
				p.body["tool_choice"] = map[string]any{"type": "auto"}
			case "gemini":
				p.body["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "AUTO"}}
			}
		}
	}
	return p
}

type referenceCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type referenceResult struct {
	text                             string
	finish                           string
	calls                            []referenceCall
	input, output, cached, reasoning int
	reasoningKnown, done             bool
}

func (r *referenceResult) call(t *testing.T, name string, arguments any) {
	t.Helper()
	if encoded, ok := arguments.(string); ok {
		if encoded == "" {
			encoded = "{}"
		}
		arguments = exactObject(t, []byte(encoded))
	}
	raw, err := json.Marshal(arguments, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	r.calls = append(r.calls, referenceCall{name, string(raw)})
}

func number(value any) int {
	switch v := value.(type) {
	case jsonutil.Number:
		n, _ := v.Int64()
		return int(n)
	case float64:
		return int(v)
	}
	return 0
}

func textValue(value any) string { s, _ := value.(string); return s }

func sortCalls(calls []referenceCall) {
	slices.SortFunc(calls, func(a, b referenceCall) int {
		if n := strings.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return strings.Compare(a.Arguments, b.Arguments)
	})
}
