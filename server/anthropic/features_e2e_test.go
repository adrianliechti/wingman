package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/provider/xai"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/go-chi/chi/v5"
)

type featureTransport func(*http.Request) (*http.Response, error)

func (f featureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func featureClient(t *testing.T, wire string, capture func(*http.Request, map[string]any)) *http.Client {
	t.Helper()
	return &http.Client{Transport: featureTransport(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		capture(r, body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire)), Request: r}, nil
	})}
}

func featureRouter(p provider.Completer) http.Handler {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("target", signatures.ScopedTo("target", p))
	router := chi.NewRouter()
	New(cfg).Attach(router)
	responses.New(cfg).Attach(router)
	return router
}

func featurePost(t *testing.T, router http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", path, bytes.NewReader(data)))
	return rec
}

const claudeFeatureStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":20,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-state"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12,"output_tokens_details":{"thinking_tokens":6}}}

event: message_stop
data: {"type":"message_stop"}

`

const responsesFeatureStream = `event: response.created
data: {"type":"response.created","response":{"id":"resp_test","model":"gpt-6-astra","status":"in_progress","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_test","role":"assistant","content":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_test","output_index":0,"content_index":0,"delta":"done"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_test","model":"gpt-6-astra","status":"completed","output":[],"usage":{"input_tokens":20,"output_tokens":12,"output_tokens_details":{"reasoning_tokens":6}}}}

`

func TestDefaultThinkingE2E(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var requests []map[string]any
			client := featureClient(t, claudeFeatureStream, func(_ *http.Request, body map[string]any) { requests = append(requests, body) })
			p, err := anthropic.NewCompleter("https://upstream.invalid", "claude-opus-5", anthropic.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			router := featureRouter(p)
			body := map[string]any{"model": "target", "max_tokens": 1024, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "Plan"}}}
			rec := featurePost(t, router, "/messages", body)
			if rec.Code != 200 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var blocks []any
			var usage map[string]any
			if stream {
				events, err := harness.ParseSSE(rec.Body)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range events {
					switch event.Event {
					case "content_block_start":
						blocks = append(blocks, event.Data["content_block"])
					case "content_block_delta":
						block := blocks[int(event.Data["index"].(float64))].(map[string]any)
						delta := event.Data["delta"].(map[string]any)
						for _, field := range []string{"thinking", "signature", "text"} {
							if value, ok := delta[field].(string); ok {
								previous, _ := block[field].(string)
								block[field] = previous + value
							}
						}
					case "message_delta":
						usage = event.Data["usage"].(map[string]any)
					}
				}
			} else {
				var result map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				blocks, usage = result["content"].([]any), result["usage"].(map[string]any)
			}
			if len(blocks) != 2 {
				t.Fatalf("lost default thinking: %v", blocks)
			}
			thinking := blocks[0].(map[string]any)
			if thinking["type"] != "thinking" || thinking["thinking"] != "" || thinking["signature"] == "" || thinking["signature"] == nil {
				t.Fatalf("invalid signature-only block: %v", thinking)
			}
			details, _ := usage["output_tokens_details"].(map[string]any)
			if details["thinking_tokens"] != float64(6) {
				t.Fatalf("lost default thinking usage: %v", usage)
			}
			body["stream"] = false
			body["messages"] = append(body["messages"].([]any), map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": "Continue"})
			if rec := featurePost(t, router, "/messages", body); rec.Code != 200 {
				t.Fatalf("replay: %s", rec.Body)
			}
			replayed := requests[1]["messages"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if replayed["signature"] != "signed-state" || replayed["thinking"] != "" {
				t.Fatalf("corrupt signature replay: %v", replayed)
			}
		})
	}
}

func TestEffortAndStrictToolsAcrossProvidersE2E(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	for _, backend := range []string{"claude", "claude-fallback", "responses", "chat", "gemini", "xai", "bedrock"} {
		for _, api := range []string{"messages", "responses"} {
			t.Run(backend+"/"+api, func(t *testing.T) {
				var sent map[string]any
				var headers http.Header
				p := featureBackend(t, backend, func(r *http.Request, body map[string]any) { sent, headers = body, r.Header })
				schema := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}
				history := []any{map[string]any{"role": "user", "content": "Plan"}, map[string]any{"role": "assistant", "content": "Ready"}, map[string]any{"role": "user", "content": "Continue"}}
				body := map[string]any{"model": "target"}
				if api == "messages" {
					body["max_tokens"] = 1024
					body["output_config"] = map[string]any{"effort": "high"}
					body["messages"] = append(history, map[string]any{"role": "system", "content": "Keep it brief.", "output_config": map[string]any{"effort": "low"}})
					body["tools"] = []any{map[string]any{"name": "lookup", "input_schema": schema, "strict": true}}
				} else {
					body["reasoning"] = map[string]any{"effort": "high"}
					body["input"] = append(history, map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": "low"}}, map[string]any{"role": "system", "content": "Keep it brief."})
					body["tools"] = []any{map[string]any{"type": "function", "name": "lookup", "parameters": schema, "strict": true}}
				}
				rec := featurePost(t, featureRouter(p), "/"+api, body)
				if rec.Code != 200 || !strings.Contains(rec.Body.String(), "done") {
					t.Fatalf("status %d: %s", rec.Code, rec.Body)
				}
				serialized, _ := json.Marshal(sent)
				if !strings.Contains(string(serialized), "Keep it brief.") {
					t.Fatalf("effort update dropped accompanying instructions: %s", serialized)
				}
				var effort any
				switch backend {
				case "claude":
					if !strings.Contains(headers.Get("anthropic-beta"), "mid-conversation-output-config-2026-07-01") {
						t.Fatal("missing effort beta")
					}
					if sent["output_config"].(map[string]any)["effort"] != "high" {
						t.Fatal("changed prefix effort")
					}
					effort = sent["messages"].([]any)[3].(map[string]any)["output_config"].(map[string]any)["effort"]
				case "claude-fallback":
					effort = sent["output_config"].(map[string]any)["effort"]
				case "responses":
					if sent["reasoning"].(map[string]any)["effort"] != "high" {
						t.Fatal("changed prefix effort")
					}
					update := sent["input"].([]any)[3].(map[string]any)
					if update["type"] != "configuration_update" {
						t.Fatalf("lost positional update: %v", update)
					}
					effort = update["reasoning"].(map[string]any)["effort"]
				case "chat":
					effort = sent["reasoning_effort"]
				case "gemini":
					effort = strings.ToLower(sent["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)["thinkingLevel"].(string))
				case "xai":
					effort = sent["reasoning"].(map[string]any)["effort"]
				case "bedrock":
					effort = sent["additionalModelRequestFields"].(map[string]any)["output_config"].(map[string]any)["effort"]
				}
				if effort != "low" {
					t.Fatalf("lost current effort: %s", serialized)
				}
				if backend == "bedrock" {
					tool := sent["toolConfig"].(map[string]any)["tools"].([]any)[0].(map[string]any)["toolSpec"].(map[string]any)
					if tool["strict"] != true {
						t.Fatalf("lost strict tool: %v", tool)
					}
				} else if backend == "gemini" {
					mode := sent["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"]
					if mode != "VALIDATED" {
						t.Fatalf("strict tools need validated function calling: %v", mode)
					}
				} else {
					tool := sent["tools"].([]any)[0].(map[string]any)
					if backend == "chat" {
						tool = tool["function"].(map[string]any)
					}
					if tool["strict"] != true {
						t.Fatalf("lost strict tool: %v", tool)
					}
				}
			})
		}
	}
}

func TestUnsupportedFeaturesRejectedE2E(t *testing.T) {
	p, _ := anthropic.NewCompleter("https://upstream.invalid", "claude-fable-5-1", anthropic.WithClient(featureClient(t, claudeFeatureStream, func(_ *http.Request, _ map[string]any) { t.Fatal("invalid request reached upstream") })))
	router := featureRouter(p)
	for _, fields := range []string{
		`"messages":[{"role":"user","content":"Temporary","clear_at":"next_user_message"}]`,
		`"thinking":{"type":"adaptive","display":"updates"}`,
		`"thinking":{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"error"}}`,
		`"output_config":{"task_budget":{"type":"tokens","total":20000}}`,
		`"tools":[{"type":"computer_toolset_20260801"}]`,
		`"tools":[{"type":"browser_toolset_20260801"}]`,
		`"messages":[{"role":"system","content":[{"type":"tool_removal","name":"lookup"}]}]`,
		`"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":2}}]}`,
		`"context_management":{"edits":[{"type":"compact_20260112","pause_after_compaction":true}]}`,
		`"tool_choice":{"type":"any"},"tools":[{"name":"lookup","input_schema":{"type":"object"}}]`,
		`"tool_choice":{"type":"invalid"}`,
		`"thinking":{"type":"invalid"}`,
		`"output_config":{"effort":"invalid"}`,
	} {
		t.Run(fields, func(t *testing.T) {
			body := `{"model":"target","max_tokens":128,"messages":[{"role":"user","content":"Hi"}],` + fields + `}`
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest("POST", "/messages", strings.NewReader(body)))
			if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_request_error") {
				t.Fatalf("unsupported control accepted: %d %s", rec.Code, rec.Body)
			}
		})
	}
	for _, body := range []string{
		`{"max_tokens":128,"messages":[{"role":"user","content":"Hi"}]}`,
		`{"model":"target","max_tokens":128,"messages":[]}`,
		`{"model":"target","max_tokens":128,"messages":[{"role":"user","content":"Hi"}]} {}`,
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("POST", "/messages", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("invalid request accepted: %d %s", rec.Code, rec.Body)
		}
	}
}

func featureBackend(t *testing.T, backend string, capture func(*http.Request, map[string]any)) provider.Completer {
	t.Helper()
	wire := claudeFeatureStream
	switch backend {
	case "responses", "xai":
		wire = responsesFeatureStream
	case "chat":
		wire = "data: {\"id\":\"chatcmpl_test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	case "gemini":
		wire = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"done\"}]},\"finishReason\":\"STOP\"}]}\n\n"
	case "bedrock":
		var buffer bytes.Buffer
		encoder := eventstream.NewEncoder()
		for _, event := range []struct{ kind, payload string }{
			{"messageStart", `{"role":"assistant"}`},
			{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"done"}}`},
			{"contentBlockStop", `{"contentBlockIndex":0}`},
			{"messageStop", `{"stopReason":"end_turn"}`},
		} {
			if err := encoder.Encode(&buffer, eventstream.Message{Headers: eventstream.Headers{
				{Name: ":message-type", Value: eventstream.StringValue("event")},
				{Name: ":event-type", Value: eventstream.StringValue(event.kind)},
				{Name: ":content-type", Value: eventstream.StringValue("application/json")},
			}, Payload: []byte(event.payload)}); err != nil {
				t.Fatal(err)
			}
		}
		wire = buffer.String()
	}
	client := featureClient(t, wire, capture)
	var p provider.Completer
	var err error
	switch backend {
	case "claude", "claude-fallback":
		model := "claude-opus-5"
		if backend == "claude-fallback" {
			model = "claude-sonnet-4-6"
		}
		p, err = anthropic.NewCompleter("https://upstream.invalid", model, anthropic.WithClient(client))
	case "responses":
		p, err = openai.NewResponder("https://upstream.invalid", "gpt-6-astra", openai.WithClient(client))
	case "chat":
		p, err = openai.NewCompleter("https://upstream.invalid", "gpt-6-astra", openai.WithClient(client))
	case "gemini":
		p, err = google.NewCompleter("gemini-3.8-flash", google.WithToken("test"), google.WithClient(client))
	case "xai":
		p, err = xai.NewCompleter("grok-4", xai.WithToken("test"), xai.WithClient(client))
	case "bedrock":
		p, err = bedrock.NewCompleter("anthropic.claude-opus-4-6-v1", bedrock.WithClient(client))
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}
