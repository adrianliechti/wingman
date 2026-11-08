package compatibility_test

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	claude "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	backend "github.com/adrianliechti/wingman/pkg/provider/openai"
	anthropicsdk "github.com/anthropics/anthropic-sdk-go/v2"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/v2/option"
	openaisdk "github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// These are real HTTP -> server adapter -> provider adapter -> HTTP round trips.
// Fixed upstream responses make exact SDK field comparisons reproducible without
// credentials or differences between independent model generations.
func TestRepairSDKCrossProvider(t *testing.T) {
	for _, upstream := range []string{"openai", "anthropic"} {
		for _, frontend := range []string{"responses", "messages"} {
			for _, reject := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-via-%s/reject=%t", upstream, frontend, reject), func(t *testing.T) {
					var baseline *repairSDKResult
					for _, stream := range []bool{false, true} {
						t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
							var mu sync.Mutex
							var requests []map[string]any
							rejected := false
							upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								data, _ := io.ReadAll(r.Body)
								var body map[string]any
								if err := json.Unmarshal(data, &body); err != nil {
									t.Error(err)
									w.WriteHeader(500)
									return
								}
								mu.Lock()
								requests = append(requests, body)
								hasSignature := strings.Contains(string(data), "opaque")
								fail := reject && hasSignature && !rejected
								if fail {
									rejected = true
								}
								mu.Unlock()
								if hasSignature && strings.Contains(string(data), "@alias:") {
									t.Error("gateway realm leaked to upstream")
								}
								if fail {
									w.Header().Set("Content-Type", "application/json")
									w.WriteHeader(400)
									if upstream == "openai" {
										io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"Encrypted content could not be verified"}}`)
									} else {
										io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`)
									}
									return
								}
								w.Header().Set("Content-Type", "text/event-stream")
								io.WriteString(w, repairUpstreamFixture(upstream))
							}))
							defer upstreamServer.Close()
							var completer provider.Completer
							var err error
							if upstream == "openai" {
								completer, err = backend.NewResponder(upstreamServer.URL, "gpt-test", backend.WithToken("test"), backend.WithMaxRetries(0))
							} else {
								completer, err = claude.NewCompleter(upstreamServer.URL, "claude-test", claude.WithToken("test"), claude.WithMaxRetries(0))
							}
							if err != nil {
								t.Fatal(err)
							}
							cfg := &config.Config{Policy: noop.New()}
							cfg.RegisterCompleter("alias", signatures.ScopedTo("alias", completer))
							endpoint := localServer(t, cfg)
							first := repairSDKCall(t, endpoint.BaseURL, frontend, stream, nil)
							if first.answer != "Hello 世界 👋" || first.thinking != "Plan" || first.signature == "" || first.callID != "call_lookup" || first.name != "lookup" || first.arguments != `{"q":"x"}` || first.input != 25 || first.outputTokens != 12 {
								t.Fatalf("SDK lost fields: %+v", first)
							}
							if baseline == nil {
								baseline = &first
							} else if first.comparable() != baseline.comparable() {
								t.Fatalf("SDK stream/JSON mismatch:\n%+v\n%+v", *baseline, first)
							}
							second := repairSDKCall(t, endpoint.BaseURL, frontend, stream, first.output)
							if second.signature == "" || second.comparable() != first.comparable() {
								t.Fatalf("replay changed SDK fields:\n%+v\n%+v", first, second)
							}
							mu.Lock()
							defer mu.Unlock()
							want := 2
							if reject {
								want = 3
							}
							if len(requests) != want {
								t.Fatalf("requests=%d want %d", len(requests), want)
							}
							replay, _ := json.Marshal(requests[1])
							if !strings.Contains(string(replay), "opaque") || !strings.Contains(string(replay), "result-value") || !strings.Contains(string(replay), "call_lookup") {
								t.Fatalf("signed tool timeline lost on replay: %s", replay)
							}
							if got := repairSignatures(requests[1]); len(got) != 1 || got[0] != "opaque" {
								t.Fatalf("opaque signature changed on replay: %v", got)
							}
							if upstream == "anthropic" && !strings.Contains(string(replay), `"thinking":"Plan"`) {
								t.Fatalf("signed thinking text lost: %s", replay)
							}
							if upstream == "openai" && !strings.Contains(string(replay), `"id":"rs_plan"`) {
								t.Fatalf("reasoning item id lost through Messages API: %s", replay)
							}
							if reject {
								retry, _ := json.Marshal(requests[2])
								if strings.Contains(string(retry), "opaque") || !strings.Contains(string(retry), "result-value") || !strings.Contains(string(retry), "call_lookup") {
									t.Fatalf("retry corrupted history: %s", retry)
								}
							}
						})
					}
				})
			}
		}
	}
}

type repairSDKResult struct {
	answer, thinking, signature, callID, name, arguments string
	input, outputTokens                                  int64
	output                                               []any
}

func (r repairSDKResult) comparable() string {
	r.output = nil
	r.signature = "present"
	return fmt.Sprintf("%+v", r)
}

func repairSDKCall(t *testing.T, baseURL, frontend string, stream bool, previous []any) repairSDKResult {
	t.Helper()
	var result repairSDKResult
	if frontend == "responses" {
		input := []any{map[string]any{"role": "user", "content": "go"}}
		if previous != nil {
			input = append(input, previous...)
			input = append(input, map[string]any{"type": "function_call_output", "call_id": "call_lookup", "output": "result-value"})
		}
		client := openaisdk.NewClient(openaioption.WithBaseURL(baseURL), openaioption.WithAPIKey("test"), openaioption.WithMaxRetries(0))
		opts := []openaioption.RequestOption{openaioption.WithJSONSet("input", input), openaioption.WithJSONSet("include", []string{"reasoning.encrypted_content"}), openaioption.WithJSONSet("reasoning", map[string]any{"effort": "low", "summary": "auto"})}
		params := responses.ResponseNewParams{Model: "alias"}
		var response *responses.Response
		if stream {
			events := client.Responses.NewStreaming(t.Context(), params, opts...)
			defer events.Close()
			var deltas strings.Builder
			for events.Next() {
				event := events.Current()
				if event.Type == "response.output_text.delta" {
					deltas.WriteString(event.Delta)
				}
				if event.Type == "response.completed" {
					r := event.Response
					response = &r
				}
			}
			if err := events.Err(); err != nil {
				t.Fatal(err)
			}
			if response == nil {
				t.Fatal("SDK saw no completed response")
			}
			if deltas.String() != response.OutputText() {
				t.Fatalf("SDK deltas %q disagree with final text %q", deltas.String(), response.OutputText())
			}
		} else {
			var err error
			response, err = client.Responses.New(t.Context(), params, opts...)
			if err != nil {
				t.Fatal(err)
			}
		}
		result.answer = response.OutputText()
		result.input = response.Usage.InputTokens
		result.outputTokens = response.Usage.OutputTokens
		if response.Status != "completed" {
			t.Fatalf("status=%s", response.Status)
		}
		for _, item := range response.Output {
			switch item.Type {
			case "reasoning":
				for _, part := range item.Summary {
					result.thinking += part.Text
				}
				result.signature = item.EncryptedContent
			case "function_call":
				result.callID, result.name, result.arguments = item.CallID, item.Name, item.AsFunctionCall().Arguments
			}
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(response.RawJSON()), &raw); err != nil {
			t.Fatal(err)
		}
		result.output = raw["output"].([]any)
	} else {
		input := []any{map[string]any{"role": "user", "content": "go"}}
		if previous != nil {
			input = append(input, map[string]any{"role": "assistant", "content": previous}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_lookup", "content": "result-value"}}})
		}
		// The SDK is used as it would be by a native Anthropic client.
		client := anthropicsdk.NewClient(anthropicoption.WithBaseURL(strings.TrimSuffix(baseURL, "/v1")), anthropicoption.WithAPIKey("test"), anthropicoption.WithMaxRetries(0))
		opts := []anthropicoption.RequestOption{anthropicoption.WithJSONSet("messages", input)}
		params := anthropicsdk.MessageNewParams{Model: "alias", MaxTokens: 256}
		var message *anthropicsdk.Message
		if stream {
			events := client.Messages.NewStreaming(t.Context(), params, opts...)
			defer events.Close()
			message = &anthropicsdk.Message{}
			for events.Next() {
				if err := message.Accumulate(events.Current()); err != nil {
					t.Fatal(err)
				}
			}
			if err := events.Err(); err != nil {
				t.Fatal(err)
			}
		} else {
			var err error
			message, err = client.Messages.New(t.Context(), params, opts...)
			if err != nil {
				t.Fatal(err)
			}
		}
		result.input = message.Usage.InputTokens
		result.outputTokens = message.Usage.OutputTokens
		if message.StopReason != "tool_use" {
			t.Fatalf("stop reason=%s", message.StopReason)
		}
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				result.answer += block.Text
			case "thinking":
				result.thinking += block.Thinking
				result.signature = block.Signature
			case "tool_use":
				result.callID, result.name = block.ID, block.Name
				var value any
				if err := json.Unmarshal(block.Input, &value); err != nil {
					t.Fatal(err)
				}
				canonical, _ := json.Marshal(value)
				result.arguments = string(canonical)
			}
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(message.RawJSON()), &raw); err != nil {
			t.Fatal(err)
		}
		result.output = raw["content"].([]any)
	}
	return result
}

func repairUpstreamFixture(upstream string) string {
	if upstream == "openai" {
		return strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_fixed","model":"gpt-test","status":"in_progress","output":[]}}`,
			`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_plan","summary_index":0,"delta":"Plan"}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"rs_plan","type":"reasoning","summary":[{"type":"summary_text","text":"Plan"}],"encrypted_content":"opaque"}}`,
			`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_answer","role":"assistant","phase":"final_answer","content":[]}}`,
			`data: {"type":"response.output_text.delta","item_id":"msg_answer","output_index":1,"content_index":0,"delta":"Hello"}`,
			`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_answer","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Hello 世界 👋","annotations":[]}]}}`,
			`data: {"type":"response.completed","response":{"id":"resp_fixed","model":"gpt-test","status":"completed","output":[{"id":"rs_plan","type":"reasoning","summary":[{"type":"summary_text","text":"Plan"}],"encrypted_content":"opaque"},{"type":"message","id":"msg_answer","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Hello 世界 👋","annotations":[]}]},{"type":"function_call","id":"fc_lookup","call_id":"call_lookup","name":"lookup","arguments":"{\"q\":\"x\"}","status":"completed"}],"usage":{"input_tokens":25,"output_tokens":12,"total_tokens":37}}}`,
		}, "\n\n") + "\n\n"
	}
	events := []string{
		`{"type":"message_start","message":{"id":"msg_fixed","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"usage":{"input_tokens":25,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello 世界 👋"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_lookup","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"x\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":12}}`,
		`{"type":"message_stop"}`,
	}
	var stream strings.Builder
	for _, event := range events {
		var value map[string]any
		json.Unmarshal([]byte(event), &value)
		fmt.Fprintf(&stream, "event: %s\ndata: %s\n\n", value["type"], event)
	}
	return stream.String()
}

// Inspect the actual provider request, not just the SDK's display of a signature.
func repairSignatures(value any) []string {
	var result []string
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "signature" || key == "encrypted_content" {
				if text, ok := child.(string); ok && text != "" {
					result = append(result, text)
				}
			} else {
				result = append(result, repairSignatures(child)...)
			}
		}
	case []any:
		for _, child := range value {
			result = append(result, repairSignatures(child)...)
		}
	}
	return result
}

// Even a recognizable signature error must not restart an already visible turn.
// Exercise each actual backend parser and each server's JSON/SSE error path.
func TestRepairSDKDoesNotRetryAfterOutput(t *testing.T) {
	for _, upstream := range []string{"openai", "anthropic"} {
		for _, frontend := range []string{"responses", "messages"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-via-%s/stream=%t", upstream, frontend, stream), func(t *testing.T) {
					var mu sync.Mutex
					calls := 0
					upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						calls++
						mu.Unlock()
						w.Header().Set("Content-Type", "text/event-stream")
						if upstream == "openai" {
							io.WriteString(w, "data: "+`{"type":"response.output_text.delta","item_id":"msg_partial","delta":"Partial"}`+"\n\n")
							io.WriteString(w, "data: "+`{"type":"error","code":"invalid_encrypted_content","message":"Encrypted content could not be verified"}`+"\n\n")
						} else {
							// The real Anthropic SDK/parser must see the opening lifecycle first.
							for _, event := range []string{
								`{"type":"message_start","message":{"id":"msg_partial","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
								`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
								`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Partial"}}`,
								`{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`,
							} {
								var value map[string]any
								json.Unmarshal([]byte(event), &value)
								fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value["type"], event)
							}
						}
					}))
					defer upstreamServer.Close()
					var completer provider.Completer
					var err error
					if upstream == "openai" {
						completer, err = backend.NewResponder(upstreamServer.URL, "gpt-test", backend.WithToken("test"), backend.WithMaxRetries(0))
					} else {
						completer, err = claude.NewCompleter(upstreamServer.URL, "claude-test", claude.WithToken("test"), claude.WithMaxRetries(0))
					}
					if err != nil {
						t.Fatal(err)
					}
					cfg := &config.Config{Policy: noop.New()}
					cfg.RegisterCompleter("alias", signatures.ScopedTo("alias", completer))
					endpoint := localServer(t, cfg)
					failed := false
					var text strings.Builder
					if frontend == "responses" {
						client := openaisdk.NewClient(openaioption.WithBaseURL(endpoint.BaseURL), openaioption.WithAPIKey("test"), openaioption.WithMaxRetries(0))
						params := responses.ResponseNewParams{Model: "alias"}
						opts := []openaioption.RequestOption{openaioption.WithJSONSet("input", []any{
							map[string]any{"role": "user", "content": "first"},
							map[string]any{"type": "reasoning", "id": "rs_old", "encrypted_content": "@alias:opaque", "summary": []any{map[string]any{"type": "summary_text", "text": "Plan"}}},
							map[string]any{"role": "assistant", "content": "answer"}, map[string]any{"role": "user", "content": "next"},
						})}
						if stream {
							events := client.Responses.NewStreaming(t.Context(), params, opts...)
							defer events.Close()
							for events.Next() {
								event := events.Current()
								if event.Type == "response.output_text.delta" {
									text.WriteString(event.Delta)
								}
								if event.Type == "response.failed" || event.Type == "error" {
									failed = true
								}
								if event.Type == "response.completed" {
									t.Fatal("failed turn marked complete")
								}
							}
							failed = failed || events.Err() != nil
						} else {
							_, err := client.Responses.New(t.Context(), params, opts...)
							failed = err != nil
						}
					} else {
						client := anthropicsdk.NewClient(anthropicoption.WithBaseURL(strings.TrimSuffix(endpoint.BaseURL, "/v1")), anthropicoption.WithAPIKey("test"), anthropicoption.WithMaxRetries(0))
						params := anthropicsdk.MessageNewParams{Model: "alias", MaxTokens: 256}
						opts := []anthropicoption.RequestOption{anthropicoption.WithJSONSet("messages", []any{
							map[string]any{"role": "user", "content": "first"},
							map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "Plan", "signature": "@alias:opaque"}, map[string]any{"type": "text", "text": "answer"}}},
							map[string]any{"role": "user", "content": "next"},
						})}
						if stream {
							events := client.Messages.NewStreaming(t.Context(), params, opts...)
							defer events.Close()
							for events.Next() {
								event := events.Current()
								if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
									text.WriteString(event.Delta.Text)
								}
							}
							failed = events.Err() != nil
						} else {
							_, err := client.Messages.New(t.Context(), params, opts...)
							failed = err != nil
						}
					}
					if !failed {
						t.Fatal("official SDK accepted failed response")
					}
					if stream && text.String() != "Partial" {
						t.Fatalf("partial output was lost or duplicated: %q", text.String())
					}
					mu.Lock()
					defer mu.Unlock()
					if calls != 1 {
						t.Fatalf("retried after output: %d calls", calls)
					}
				})
			}
		}
	}
}
