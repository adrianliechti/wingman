package protocol_test

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type openAITransport func(*http.Request) (*http.Response, error)

func (f openAITransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestOpenAIFilteredResponse(t *testing.T) {
	// The upstream delivers the refusal only in its final snapshot and
	// reports a different effective service tier from the one requested.
	client := &http.Client{Transport: openAITransport(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.UnmarshalRead(req.Body, &body); err != nil {
			return nil, err
		}
		if body["safety_identifier"] != "user-hash" || body["service_tier"] != "ultrafast" {
			t.Errorf("upstream metadata = %v/%v", body["safety_identifier"], body["service_tier"])
		}
		stream := `data: {"type":"response.incomplete","response":{"status":"incomplete","service_tier":"default","incomplete_details":{"reason":"content_filter"},"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4},"output":[{"type":"message","id":"msg","role":"assistant","content":[{"type":"refusal","refusal":"declined"}]}]}}` + "\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: req}, nil
	})}
	backend, err := openai.NewResponder("", "gpt-6.2-sol", openai.WithToken("fixture"), openai.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", backend)
	local := configuredGateway(t, cfg)
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("chat=%t/stream=%t", chat, stream), func(t *testing.T) {
				body := map[string]any{"model": "test", "safety_identifier": "user-hash", "service_tier": "ultrafast", "stream": stream}
				path := "/v1/responses"
				if chat {
					path = "/v1/chat/completions"
					body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
					body["stream_options"] = map[string]any{"include_usage": true}
				} else {
					body["input"] = "hi"
				}
				status, _, raw := post(t, local, path, body)
				requireStatus(t, status, 200, raw)
				if chat && stream {
					var refusal, finish string
					for _, frame := range frames(t, raw) {
						if frame.Raw == "[DONE]" {
							continue
						}
						equal(t, "effective tier on chunk", frame.Data["service_tier"], "default")
						refusal += textValue(at(frame.Data, "choices", 0, "delta", "refusal"))
						finish += textValue(at(frame.Data, "choices", 0, "finish_reason"))
					}
					equal(t, "refusal", refusal, "declined")
					equal(t, "finish", finish, "content_filter")
					return
				}
				var out map[string]any
				if stream {
					events := frames(t, raw)
					last := events[len(events)-1]
					equal(t, "terminal event", last.Data["type"], "response.incomplete")
					out = last.Data["response"].(map[string]any)
				} else {
					out = object(t, raw)
				}
				equal(t, "effective tier", out["service_tier"], "default")
				if chat {
					equal(t, "finish", at(out, "choices", 0, "finish_reason"), "content_filter")
					equal(t, "refusal", at(out, "choices", 0, "message", "refusal"), "declined")
				} else {
					equal(t, "status", out["status"], "incomplete")
					equal(t, "reason", at(out, "incomplete_details", "reason"), "content_filter")
					equal(t, "refusal", at(out, "output", 0, "content", 0, "refusal"), "declined")
					equal(t, "safety identifier", out["safety_identifier"], "user-hash")
				}
			})
		}
	}
}

func liveOpenAIResponses(t *testing.T, model string) (*httptest.Server, *nativeCapture) {
	t.Helper()
	capture := &nativeCapture{}
	client := &http.Client{Transport: capture, Timeout: 90 * time.Second}
	backend, err := openai.NewResponder("", model, openai.WithToken(os.Getenv("OPENAI_API_KEY")), openai.WithMaxRetries(0), openai.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, backend)
	local := configuredGateway(t, cfg)
	local.Client().Timeout = client.Timeout
	return local, capture
}

func TestLiveOpenAIAdditionalTools(t *testing.T) {
	referenceLive(t, "OPENAI_API_KEY")
	local, capture := liveOpenAIResponses(t, liveOpenAIModel)
	body := map[string]any{
		"model": liveOpenAIModel,
		"input": []any{
			map[string]any{"role": "user", "content": "The location is Boston."},
			map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{
				map[string]any{"type": "function", "name": "weather", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
			}},
			map[string]any{"role": "user", "content": "Call weather now."},
		},
		"tool_choice": "required", "safety_identifier": "wingman-integration-test",
	}
	status, _, raw := post(t, local, "/v1/responses", body)
	native := capture.only(t)
	wire := object(t, native.requestBody)
	equal(t, "positional tool", at(wire, "input", 1, "tools", 0, "name"), "weather")
	equal(t, "upstream safety identifier", wire["safety_identifier"], "wingman-integration-test")
	if defs, _ := wire["tools"].([]any); len(defs) != 0 {
		t.Fatalf("positional tools also sent at top level: %v", defs)
	}
	requireStatus(t, status, 200, raw)
	result := readReference(t, "responses", false, raw)
	if !result.done || len(result.calls) != 1 || result.calls[0].Name != "weather" {
		t.Fatalf("additional tool call: %+v", result)
	}
}

func TestLiveOpenAIUltrafast(t *testing.T) {
	referenceLive(t, "OPENAI_API_KEY")
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		t.Run(model, func(t *testing.T) {
			local, _ := liveOpenAIResponses(t, model)
			status, _, raw := post(t, local, "/v1/responses", map[string]any{
				"model": model, "service_tier": "ultrafast", "input": "Reply with OK.", "reasoning": map[string]any{"effort": "low"},
			})
			requireStatus(t, status, 200, raw)
			out := object(t, raw)
			equal(t, "status", out["status"], "completed")
			equal(t, "effective service tier", out["service_tier"], "ultrafast")
		})
	}
}
