package anthropic

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/test/harness"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

// Exercise the complete request/stream/replay path, including the distinction
// between hidden reasoning and visible progress notes in separate signed blocks.
func TestProgressNotesE2E(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	for _, backend := range []string{"anthropic", "bedrock"} {
		for _, api := range []string{"messages", "responses"} {
			for _, effort := range []string{"high", "none", "minimal"} {
				if api == "messages" && effort == "minimal" {
					continue
				}
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", backend, api, effort, stream), func(t *testing.T) {
						var sent []map[string]any
						client := featureClient(t, progressWire(t, backend), func(_ *http.Request, body map[string]any) { sent = append(sent, body) })
						var p provider.Completer
						var err error
						if backend == "anthropic" {
							p, err = anthropic.NewCompleter("https://upstream.invalid", "claude-sonnet-5-5", anthropic.WithClient(client))
						} else {
							p, err = bedrock.NewCompleter("anthropic.claude-sonnet-5-5", bedrock.WithClient(client))
						}
						if err != nil {
							t.Fatal(err)
						}
						router := featureRouter(p)
						history := []any{map[string]any{"role": "user", "content": "Look it up"}}
						body := map[string]any{"model": "target", "stream": stream}
						schema := map[string]any{"type": "object", "properties": map[string]any{}}
						if api == "messages" {
							body["max_tokens"], body["messages"] = 1024, history
							body["thinking"] = map[string]any{"type": "adaptive", "display": "updates"}
							if effort == "none" {
								body["thinking"] = map[string]any{"type": "between_tools"}
							}
							body["tools"] = []any{map[string]any{"name": "lookup", "input_schema": schema}}
						} else {
							body["input"] = history
							body["reasoning"] = map[string]any{"effort": effort}
							body["tools"] = []any{map[string]any{"type": "function", "name": "lookup", "parameters": schema}}
						}
						rec := featurePost(t, router, "/"+api, body)
						output := progressOutput(t, rec, api, stream)
						if len(output) != 3 {
							t.Fatalf("want hidden state, progress note, and tool call: %v", output)
						}
						for i, text := range []string{"", "Looking it up."} {
							block := output[i].(map[string]any)
							if api == "messages" {
								if block["type"] != "thinking" || block["thinking"] != text || block["signature"] == nil || block["signature"] == "" {
									t.Fatalf("invalid thinking block: %v", block)
								}
							} else {
								if block["type"] != "reasoning" || block["encrypted_content"] == nil || block["encrypted_content"] == "" {
									t.Fatalf("lost replayable progress: %v", block)
								}
								var summary strings.Builder
								for _, part := range block["summary"].([]any) {
									summary.WriteString(part.(map[string]any)["text"].(string))
								}
								if summary.String() != text || len(block["content"].([]any)) != 0 {
									t.Fatalf("progress must be a summary: %v", block)
								}
							}
						}
						call := output[2].(map[string]any)
						if call["name"] != "lookup" {
							t.Fatalf("lost tool call: %v", call)
						}
						if api == "messages" {
							body["messages"] = append(history, map[string]any{"role": "assistant", "content": output}, map[string]any{"role": "user", "content": []any{
								map[string]any{"type": "tool_result", "tool_use_id": call["id"], "content": "Found it"},
							}})
						} else {
							body["input"] = append(append(history, output...), map[string]any{"type": "function_call_output", "call_id": call["call_id"], "output": "Found it"})
						}
						body["stream"] = false
						if rec := featurePost(t, router, "/"+api, body); rec.Code != http.StatusOK {
							t.Fatalf("replay: %d %s", rec.Code, rec.Body)
						}
						replayed := sent[1]["messages"].([]any)[1].(map[string]any)["content"].([]any)
						for i, text := range []string{"", "Looking it up."} {
							block := replayed[i].(map[string]any)
							textKey := "thinking"
							if backend == "bedrock" {
								block = block["reasoningContent"].(map[string]any)["reasoningText"].(map[string]any)
								textKey = "text"
							}
							if block[textKey] != text || block["signature"] != fmt.Sprintf("signed-%d", i) {
								t.Fatalf("corrupt signed replay: %v", block)
							}
						}
					})
				}
			}
		}
	}
}

func progressWire(t *testing.T, backend string) string {
	t.Helper()
	var wire bytes.Buffer
	encoder := eventstream.NewEncoder()
	emit := func(kind, payload string) {
		if backend == "anthropic" {
			fmt.Fprintf(&wire, "event: %s\ndata: %s\n\n", kind, payload)
			return
		}
		if err := encoder.Encode(&wire, eventstream.Message{Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("event")},
			{Name: ":event-type", Value: eventstream.StringValue(kind)},
			{Name: ":content-type", Value: eventstream.StringValue("application/json")},
		}, Payload: []byte(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	if backend == "anthropic" {
		emit("message_start", `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":20,"output_tokens":0}}}`)
		for i, text := range []string{"", "Looking it up."} {
			emit("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"thinking","thinking":"","signature":""}}`, i))
			emit("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"thinking_delta","thinking":%q}}`, i, text))
			// The adapter must join signature fragments before either frontend
			// wraps the opaque value for replay on the next turn.
			emit("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":"signed-"}}`, i))
			emit("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":"%d"}}`, i, i))
			emit("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
		}
		emit("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`)
		emit("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)
		emit("content_block_stop", `{"type":"content_block_stop","index":2}`)
		emit("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`)
		emit("message_stop", `{"type":"message_stop"}`)
	} else {
		emit("messageStart", `{"role":"assistant"}`)
		for i, text := range []string{"", "Looking it up."} {
			emit("contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"reasoningContent":{"text":%q}}}`, i, text))
			emit("contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"reasoningContent":{"signature":"signed-"}}}`, i))
			emit("contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"reasoningContent":{"signature":"%d"}}}`, i, i))
			emit("contentBlockStop", fmt.Sprintf(`{"contentBlockIndex":%d}`, i))
		}
		emit("contentBlockStart", `{"contentBlockIndex":2,"start":{"toolUse":{"toolUseId":"call_1","name":"lookup"}}}`)
		emit("contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"{}"}}}`)
		emit("contentBlockStop", `{"contentBlockIndex":2}`)
		emit("messageStop", `{"stopReason":"tool_use"}`)
		emit("metadata", `{"usage":{"inputTokens":20,"outputTokens":12,"totalTokens":32},"metrics":{"latencyMs":1}}`)
	}
	return wire.String()
}

func progressOutput(t *testing.T, rec *httptest.ResponseRecorder, api string, stream bool) []any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	key := "content"
	if api == "responses" {
		key = "output"
	}
	if !stream {
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result[key].([]any)
	}
	events, err := harness.ParseSSE(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []any
	for _, event := range events {
		switch event.Event {
		case "error":
			t.Fatalf("stream error: %v", event.Data)
		case "response.output_item.done":
			blocks = append(blocks, event.Data["item"])
		case "response.completed":
			output := event.Data["response"].(map[string]any)["output"]
			if !reflect.DeepEqual(blocks, output) {
				t.Fatalf("streamed items differ from final output: %v != %v", blocks, output)
			}
		case "content_block_start":
			blocks = append(blocks, event.Data["content_block"])
		case "content_block_delta":
			block := blocks[int(event.Data["index"].(float64))].(map[string]any)
			delta := event.Data["delta"].(map[string]any)
			for _, field := range []string{"thinking", "signature", "text", "partial_json"} {
				if value, ok := delta[field].(string); ok {
					previous, _ := block[field].(string)
					block[field] = previous + value
				}
			}
		}
	}
	for _, raw := range blocks {
		block := raw.(map[string]any)
		if partial, ok := block["partial_json"].(string); ok {
			var input any
			if err := json.Unmarshal([]byte(partial), &input); err != nil {
				t.Fatal(err)
			}
			block["input"] = input
			delete(block, "partial_json")
		}
	}
	return blocks
}
