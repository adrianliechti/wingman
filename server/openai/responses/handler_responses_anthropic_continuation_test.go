package responses

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/go-chi/chi/v5"
)

// The client sends ordinary Responses history throughout. Only the upstream
// transport is mocked: the handler, signature adapter, and Anthropic SDK run.
func TestResponsesAnthropicContinuationAndSignedReplay(t *testing.T) {
	for _, endpoint := range []string{"https://anthropic.test", "https://bedrock-mantle.eu-west-1.api.aws/anthropic", "https://bedrock-runtime.eu-west-1.amazonaws.com"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", endpoint, stream), func(t *testing.T) {
				calls := 0
				var continued []any
				client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					var request struct {
						Messages []any `json:"messages"`
					}
					if err := json.UnmarshalRead(r.Body, &request); err != nil {
						t.Fatal(err)
					}
					if calls == 1 {
						if len(request.Messages) != 4 || request.Messages[3].(map[string]any)["role"] != "assistant" {
							t.Fatalf("lost Responses assistant history: %v", request.Messages)
						}
						// Bedrock uses a top-level message; the native API nests it.
						message := "This model does not support assistant message prefill. The conversation must end with a user message."
						body, _ := json.Marshal(map[string]any{"message": message}, json.Deterministic(true))
						if !strings.Contains(endpoint, "bedrock-runtime") {
							body, _ = json.Marshal(map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": message}}, json.Deterministic(true))
						}
						return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
					}
					if calls == 2 {
						continued = request.Messages
						if len(continued) != 5 || continued[4].(map[string]any)["role"] != "user" {
							t.Fatalf("missing backend continuation: %v", continued)
						}
					} else {
						if len(request.Messages) < 7 || !reflect.DeepEqual(request.Messages[:5], continued) {
							t.Fatalf("signed conversation prefix changed: %v", request.Messages)
						}
						assistant := request.Messages[5].(map[string]any)["content"].([]any)
						thinking := assistant[0].(map[string]any)
						if thinking["signature"] != "SIG_1" || thinking["thinking"] != "Check the result." {
							t.Fatalf("native thinking changed: %v", thinking)
						}
						result := request.Messages[6].(map[string]any)["content"].([]any)[0].(map[string]any)
						if result["type"] != "tool_result" || result["tool_use_id"] != "call_2" {
							t.Fatalf("tool result lost: %v", result)
						}
					}
					body := anthropicContinuationEvents(calls == 2)
					contentType := "text/event-stream"
					if strings.Contains(endpoint, "bedrock-runtime") {
						body = anthropicContinuationBedrockEvents(t, body)
						contentType = "application/vnd.amazon.eventstream"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
				})}
				model := "anthropic.claude-sonnet-5-5"
				completer, err := anthropic.NewCompleter(endpoint, model, anthropic.WithClient(client), anthropic.WithToken("test"), anthropic.WithMaxRetries(0))
				if err != nil {
					t.Fatal(err)
				}
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter(model, signatures.ScopedTo(model, completer))
				router := chi.NewRouter()
				router.Route("/v1", New(cfg).Attach)
				server := httptest.NewServer(router)
				t.Cleanup(server.Close)
				input := []map[string]any{
					{"role": "user", "content": "Inspect the repository."},
					{"type": "function_call", "call_id": "call_1", "name": "glob", "arguments": "{}"},
					{"type": "function_call_output", "call_id": "call_1", "output": "README.md"},
					{"role": "assistant", "content": "Now I will update the docs."},
				}
				original, _ := json.Marshal(input, json.Deterministic(true))
				output := postGeminiResponses(t, server.URL, model, input, geminiReplayTools(), stream)
				unchanged, _ := json.Marshal(input, json.Deterministic(true))
				if string(original) != string(unchanged) || len(output) != 2 || output[0]["type"] != "reasoning" || output[1]["type"] != "function_call" {
					t.Fatalf("client history/output changed: %v", output)
				}
				input = append(input, output...)
				input = append(input, map[string]any{"type": "function_call_output", "call_id": "call_2", "output": "verified"})
				output = postGeminiResponses(t, server.URL, model, input, geminiReplayTools(), stream)
				input = append(input, output...)
				input = append(input, map[string]any{"role": "user", "content": "Confirm once more."})
				postGeminiResponses(t, server.URL, model, input, geminiReplayTools(), stream)
				if calls != 4 {
					t.Fatalf("upstream requests=%d, want one recovery plus three Responses calls", calls)
				}
			})
		}
	}
}

func anthropicContinuationEvents(tool bool) []byte {
	var events strings.Builder
	write := func(kind, payload string) { fmt.Fprintf(&events, "event: %s\ndata: %s\n\n", kind, payload) }
	write("message_start", `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`)
	if tool {
		write("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"Check the result.","signature":"SIG_1"}}`)
		write("content_block_stop", `{"type":"content_block_stop","index":0}`)
		write("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_2","name":"glob","input":{}}}`)
		write("content_block_stop", `{"type":"content_block_stop","index":1}`)
		write("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`)
	} else {
		write("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"DONE"}}`)
		write("content_block_stop", `{"type":"content_block_stop","index":0}`)
		write("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
	}
	write("message_stop", `{"type":"message_stop"}`)
	return []byte(events.String())
}

func anthropicContinuationBedrockEvents(t *testing.T, sse []byte) []byte {
	t.Helper()
	var body bytes.Buffer
	for line := range strings.SplitSeq(string(sse), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(data))}, json.Deterministic(true))
		message := eventstream.Message{Payload: payload}
		message.Headers.Set(":message-type", eventstream.StringValue("event"))
		message.Headers.Set(":event-type", eventstream.StringValue("chunk"))
		if err := eventstream.NewEncoder().Encode(&body, message); err != nil {
			t.Fatal(err)
		}
	}
	return body.Bytes()
}
