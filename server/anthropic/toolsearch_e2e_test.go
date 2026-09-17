package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/go-chi/chi/v5"
)

func searchStream(backend string) string {
	var wire strings.Builder
	emit := func(kind, data string) { fmt.Fprintf(&wire, "event: %s\ndata: %s\n\n", kind, data) }
	if backend == "claude" {
		emit("message_start", `{"type":"message_start","message":{"id":"msg_search","role":"assistant","model":"claude-fable-5-1","content":[],"usage":{"input_tokens":20,"output_tokens":0}}}`)
		for i, block := range []string{
			`{"type":"text","text":"Searching."}`,
			`{"type":"server_tool_use","id":"srv_1","name":"tool_search_tool_regex","input":{}}`,
			`{"type":"tool_search_tool_result","tool_use_id":"srv_1","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"lookup"}]}}`,
			`{"type":"text","text":"Found it."}`,
			`{"type":"tool_use","id":"call_1","name":"lookup","input":{}}`,
		} {
			emit("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":%s}`, i, block))
			if i == 1 {
				emit("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pattern\":\"lookup\"}"}}`)
			}
			emit("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
		}
		emit("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":30}}`)
		emit("message_stop", `{"type":"message_stop"}`)
	} else {
		emit("response.created", `{"type":"response.created","response":{"id":"resp_search","model":"gpt-6-astra","status":"in_progress","output":[]}}`)
		for i, item := range []string{
			`{"type":"tool_search_call","id":"tsc_1","call_id":null,"execution":"server","status":"completed","arguments":{"paths":["lookup"]}}`,
			`{"type":"tool_search_output","id":"tso_1","call_id":null,"execution":"server","status":"completed","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}},"defer_loading":true}]}`,
			`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}","status":"completed"}`,
		} {
			if i == 2 {
				emit("response.output_item.added", fmt.Sprintf(`{"type":"response.output_item.added","output_index":%d,"item":%s}`, i, item))
				emit("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"{}"}`)
			}
			emit("response.output_item.done", fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, i, item))
		}
		emit("response.completed", `{"type":"response.completed","response":{"id":"resp_search","model":"gpt-6-astra","status":"completed","output":[],"usage":{"input_tokens":20,"output_tokens":30}}}`)
	}
	return wire.String()
}

func TestToolSearchAcrossAPIsAndProvidersE2E(t *testing.T) {
	for _, source := range []string{"claude", "openai"} {
		for _, target := range []string{"claude", "openai"} {
			for _, api := range []string{"messages", "responses"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s-to-%s/%s/stream=%t", source, target, api, stream), func(t *testing.T) {
						requests := map[string][]map[string]any{}
						cfg := &config.Config{Policy: noop.New()}
						for _, backend := range []string{"claude", "openai"} {
							client := featureClient(t, searchStream(backend), func(_ *http.Request, body map[string]any) { requests[backend] = append(requests[backend], body) })
							if backend == "claude" {
								p, _ := anthropic.NewCompleter("https://upstream.invalid", "claude-fable-5-1", anthropic.WithClient(client))
								cfg.RegisterCompleter(backend, signatures.ScopedTo(backend, p))
							} else {
								p, _ := openai.NewResponder("https://upstream.invalid", "gpt-6-astra", openai.WithClient(client))
								cfg.RegisterCompleter(backend, signatures.ScopedTo(backend, p))
							}
						}
						router := chi.NewRouter()
						New(cfg).Attach(router)
						responses.New(cfg).Attach(router)
						body := map[string]any{"model": source, "stream": stream}
						history := []any{map[string]any{"role": "user", "content": "Use lookup."}}
						schema := map[string]any{"type": "object", "properties": map[string]any{}}
						if api == "messages" {
							body["max_tokens"], body["messages"] = 1024, history
							body["tools"] = []any{map[string]any{"type": "tool_search_tool_regex_20251119", "name": "tool_search_tool_regex"}, map[string]any{"name": "lookup", "input_schema": schema, "defer_loading": true}}
						} else {
							body["input"] = history
							body["tools"] = []any{map[string]any{"type": "tool_search"}, map[string]any{"type": "function", "name": "lookup", "parameters": schema, "defer_loading": true}}
						}
						rec := featurePost(t, router, "/"+api, body)
						if rec.Code != 200 {
							t.Fatalf("initial request: %d %s", rec.Code, rec.Body)
						}
						var output []any
						if stream {
							events, _ := harness.ParseSSE(rec.Body)
							for _, event := range events {
								switch event.Event {
								case "content_block_start":
									output = append(output, event.Data["content_block"])
								case "content_block_delta":
									block := output[int(event.Data["index"].(float64))].(map[string]any)
									delta := event.Data["delta"].(map[string]any)
									if delta["type"] == "input_json_delta" {
										var input any
										if err := json.Unmarshal([]byte(delta["partial_json"].(string)), &input); err != nil {
											t.Fatal(err)
										}
										block["input"] = input
									} else if delta["type"] == "text_delta" {
										previous, _ := block["text"].(string)
										block["text"] = previous + delta["text"].(string)
									}
								case "response.completed":
									output = event.Data["response"].(map[string]any)["output"].([]any)
								case "error":
									t.Fatalf("stream error: %v", event.Data)
								}
							}
						} else {
							var result map[string]any
							json.Unmarshal(rec.Body.Bytes(), &result)
							field := "content"
							if api == "responses" {
								field = "output"
							}
							output, _ = result[field].([]any)
						}
						var types []string
						for _, raw := range output {
							types = append(types, raw.(map[string]any)["type"].(string))
						}
						data, _ := json.Marshal(output)
						searchID := "srv_1"
						if source == "openai" {
							searchID = "tsc_1"
						}
						if !strings.Contains(string(data), "tool_search") || !strings.Contains(string(data), searchID) || !strings.Contains(string(data), "lookup") {
							t.Fatalf("missing search state: %s", data)
						}
						if source == "claude" && api == "messages" && !reflect.DeepEqual(types, []string{"text", "server_tool_use", "tool_search_tool_result", "text", "tool_use"}) {
							t.Fatalf("changed signed block order: %v", types)
						}
						body["model"], body["stream"] = target, false
						if api == "messages" {
							body["messages"] = append(history, map[string]any{"role": "assistant", "content": output}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "Done"}}})
						} else {
							body["input"] = append(append(history, output...), map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "Done"})
						}
						rec = featurePost(t, router, "/"+api, body)
						if rec.Code != 200 {
							t.Fatalf("replay: %d %s", rec.Code, rec.Body)
						}
						sent := requests[target][len(requests[target])-1]
						data, _ = json.Marshal(sent)
						if target == "claude" {
							tool := sent["tools"].([]any)[1].(map[string]any)
							if tool["defer_loading"] != true || !strings.Contains(string(data), "tool_search_tool_result") {
								t.Fatalf("lost deferred replay: %s", data)
							}
						} else {
							var sawResult, sawCall bool
							for _, raw := range sent["input"].([]any) {
								item := raw.(map[string]any)
								if item["type"] == "tool_search_output" {
									tool := item["tools"].([]any)[0].(map[string]any)
									if tool["parameters"] == nil || tool["defer_loading"] != true || item["call_id"] != nil {
										t.Fatalf("invalid loaded definition: %v", item)
									}
									sawResult = true
								}
								if item["type"] == "function_call" {
									if item["namespace"] != "lookup" {
										t.Fatalf("lost loaded-tool namespace: %v", item)
									}
									sawCall = true
								}
							}
							if !sawResult || !sawCall {
								t.Fatalf("lost search replay: %s", data)
							}
						}
					})
				}
			}
		}
	}
}

func TestToolSearchFallbackAcrossProvidersE2E(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	for _, backend := range []string{"chat", "gemini", "xai", "bedrock"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", backend, stream), func(t *testing.T) {
				var sent map[string]any
				p := featureBackend(t, backend, func(_ *http.Request, body map[string]any) { sent = body })
				body := map[string]any{"model": "target", "stream": stream, "max_tokens": 1024,
					"tools": []any{
						map[string]any{"type": "tool_search_tool_regex_20251119", "name": "tool_search_tool_regex"},
						map[string]any{"name": "lookup", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}}, "defer_loading": true},
					},
					"messages": []any{
						map[string]any{"role": "user", "content": "Use lookup."},
						map[string]any{"role": "assistant", "content": []any{
							map[string]any{"type": "server_tool_use", "id": "srvtoolu_1", "name": "tool_search_tool_regex", "input": map[string]any{"pattern": "lookup"}},
							map[string]any{"type": "tool_search_tool_result", "tool_use_id": "srvtoolu_1", "content": map[string]any{"type": "tool_search_tool_search_result", "tool_references": []any{map[string]any{"type": "tool_reference", "tool_name": "lookup"}}}},
							map[string]any{"type": "tool_use", "id": "call_1", "name": "lookup", "input": map[string]any{}},
						}},
						map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "READY-7"}}},
					},
				}
				rec := featurePost(t, featureRouter(p), "/messages", body)
				if rec.Code != 200 || !strings.Contains(rec.Body.String(), "done") {
					t.Fatalf("fallback failed: %d %s", rec.Code, rec.Body)
				}
				data, _ := json.Marshal(sent)
				wire := string(data)
				if strings.Contains(wire, "tool_search") || !strings.Contains(wire, "lookup") || !strings.Contains(wire, "READY-7") || !strings.Contains(wire, "call_1") {
					t.Fatalf("fallback lost tool state: %s", wire)
				}
			})
		}
	}
}
