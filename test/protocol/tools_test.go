package protocol_test

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

type toolCall struct{ id, name, arguments string }

func TestParallelToolRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, p := range protocols(stream) {
			t.Run(fmt.Sprintf("%s/stream=%v", p.name, stream), func(t *testing.T) {
				var requests atomic.Int32
				local := gateway(t, func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.UnmarshalRead(r.Body, &body); err != nil {
						t.Error(err)
						return
					}
					if at(body, "tools", 0, "function", "name") != "weather" || at(body, "tools", 0, "function", "parameters", "required", 0) != "city" {
						t.Errorf("tool definition lost: %v", body["tools"])
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if requests.Add(1) == 1 {
						for _, fragment := range []struct {
							index               int
							id, name, arguments string
						}{
							{0, "call_zurich", "weather", `{"city":"`}, {1, "call_tokyo", "weather", `{"city":"`},
							{1, "", "", `東京"}`}, {0, "", "", `Zürich"}`},
						} {
							call := map[string]any{"index": fragment.index, "function": map[string]any{"arguments": fragment.arguments}}
							if fragment.id != "" {
								call["id"] = fragment.id
								call["type"] = "function"
								call["function"].(map[string]any)["name"] = fragment.name
							}
							writeChunk(w, map[string]any{"role": "assistant", "tool_calls": []any{call}}, nil, nil)
						}
						writeChunk(w, map[string]any{}, "tool_calls", nil)
					} else {
						results := map[string]string{}
						messages, _ := body["messages"].([]any)
						for _, message := range messages {
							if at(message, "role") == "tool" {
								id, _ := at(message, "tool_call_id").(string)
								content, _ := at(message, "content").(string)
								if p.name == "gemini" {
									var response struct {
										Output string `json:"output"`
									}
									if err := json.Unmarshal([]byte(content), &response); err != nil {
										t.Error(err)
									}
									content = response.Output
								}
								results[id] = content
							}
						}
						if results["call_zurich"] != "zurich-result" || results["call_tokyo"] != "tokyo-result" {
							t.Errorf("tool results lost their call IDs: %v", body)
						}
						writeChunk(w, map[string]any{"role": "assistant", "content": answer}, "stop", nil)
					}
					writeChunk(w, nil, nil, map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12})
					io.WriteString(w, "data: [DONE]\n\n")
				})
				schema := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}}
				function := map[string]any{"name": "weather", "parameters": schema}
				switch p.name {
				case "chat":
					p.body["tools"] = []any{map[string]any{"type": "function", "function": function}}
				case "responses":
					p.body["tools"] = []any{map[string]any{"type": "function", "name": "weather", "parameters": schema}}
				case "anthropic":
					p.body["tools"] = []any{map[string]any{"name": "weather", "input_schema": schema}}
				case "gemini":
					p.body["tools"] = []any{map[string]any{"functionDeclarations": []any{function}}}
				}
				status, _, raw := post(t, local, p.path, p.body)
				requireStatus(t, status, 200, raw)
				calls := readToolCalls(t, p.name, stream, raw)
				equal(t, "tool call count", len(calls), 2)
				for i, city := range []string{"Zürich", "東京"} {
					equal(t, "tool name", calls[i].name, "weather")
					equal(t, "tool argument", object(t, []byte(calls[i].arguments))["city"], city)
				}
				equal(t, "first id", calls[0].id, "call_zurich")
				equal(t, "second id", calls[1].id, "call_tokyo")
				appendToolResults(t, &p, calls)
				status, _, raw = post(t, local, p.path, p.body)
				requireStatus(t, status, 200, raw)
				if stream {
					checkStream(t, p.name, "stop", raw)
				} else {
					checkCompletion(t, p.name, "stop", object(t, raw))
				}
				equal(t, "upstream requests", requests.Load(), int32(2))
			})
		}
	}
}

func readToolCalls(t *testing.T, protocol string, stream bool, raw []byte) []toolCall {
	t.Helper()
	var body map[string]any
	var calls []toolCall
	if stream {
		for _, e := range frames(t, raw) {
			switch protocol {
			case "responses":
				if e.Event == "response.completed" {
					body, _ = e.Data["response"].(map[string]any)
				}
			case "gemini":
				if at(e.Data, "candidates", 0, "finishReason") != nil {
					body = e.Data
				}
			case "chat":
				items, _ := at(e.Data, "choices", 0, "delta", "tool_calls").([]any)
				for _, item := range items {
					index, ok := at(item, "index").(float64)
					if !ok {
						t.Fatalf("missing tool index: %v", item)
					}
					i := int(index)
					if i < 0 || i > 1 {
						t.Fatalf("unexpected tool index: %v", index)
					}
					for len(calls) <= i {
						calls = append(calls, toolCall{})
					}
					if id, ok := at(item, "id").(string); ok {
						calls[i].id += id
					}
					if name, ok := at(item, "function", "name").(string); ok {
						calls[i].name += name
					}
					if args, ok := at(item, "function", "arguments").(string); ok {
						calls[i].arguments += args
					}
				}
			case "anthropic":
				if e.Event != "content_block_start" && e.Event != "content_block_delta" {
					continue
				}
				index, ok := e.Data["index"].(float64)
				if !ok {
					t.Fatalf("missing block index: %v", e.Data)
				}
				i := int(index)
				if i < 0 || i > 1 {
					t.Fatalf("unexpected block index: %v", index)
				}
				for len(calls) <= i {
					calls = append(calls, toolCall{})
				}
				if e.Event == "content_block_start" {
					calls[i].id, _ = at(e.Data, "content_block", "id").(string)
					calls[i].name, _ = at(e.Data, "content_block", "name").(string)
				} else if partial, ok := at(e.Data, "delta", "partial_json").(string); ok {
					calls[i].arguments += partial
				}
			}
		}
		if protocol == "chat" || protocol == "anthropic" {
			return calls
		}
	} else {
		body = object(t, raw)
	}
	var items []any
	switch protocol {
	case "chat":
		items, _ = at(body, "choices", 0, "message", "tool_calls").([]any)
	case "responses":
		items, _ = body["output"].([]any)
	case "anthropic":
		items, _ = body["content"].([]any)
	case "gemini":
		items, _ = at(body, "candidates", 0, "content", "parts").([]any)
	}
	for _, item := range items {
		var call toolCall
		switch protocol {
		case "chat":
			call.id, _ = at(item, "id").(string)
			call.name, _ = at(item, "function", "name").(string)
			call.arguments, _ = at(item, "function", "arguments").(string)
		case "responses":
			call.id, _ = at(item, "call_id").(string)
			call.name, _ = at(item, "name").(string)
			call.arguments, _ = at(item, "arguments").(string)
		case "anthropic":
			call.id, _ = at(item, "id").(string)
			call.name, _ = at(item, "name").(string)
			encoded, err := json.Marshal(at(item, "input"))
			if err != nil {
				t.Fatal(err)
			}
			call.arguments = string(encoded)
		case "gemini":
			call.id, _ = at(item, "functionCall", "id").(string)
			call.name, _ = at(item, "functionCall", "name").(string)
			encoded, err := json.Marshal(at(item, "functionCall", "args"))
			if err != nil {
				t.Fatal(err)
			}
			call.arguments = string(encoded)
		}
		calls = append(calls, call)
	}
	return calls
}

func appendToolResults(t *testing.T, p *protocol, calls []toolCall) {
	t.Helper()
	var invocation, results []any
	for _, c := range calls {
		args := object(t, []byte(c.arguments))
		switch p.name {
		case "chat":
			invocation = append(invocation, map[string]any{"id": c.id, "type": "function", "function": map[string]any{"name": c.name, "arguments": c.arguments}})
		case "responses":
			invocation = append(invocation, map[string]any{"type": "function_call", "call_id": c.id, "name": c.name, "arguments": c.arguments})
		case "anthropic":
			invocation = append(invocation, map[string]any{"type": "tool_use", "id": c.id, "name": c.name, "input": args})
		case "gemini":
			invocation = append(invocation, map[string]any{"functionCall": map[string]any{"id": c.id, "name": c.name, "args": args}})
		}
	}
	// Return parallel results in reverse order to verify correlation by ID.
	for _, c := range slices.Backward(calls) {
		result := strings.TrimPrefix(c.id, "call_") + "-result"
		switch p.name {
		case "chat":
			results = append(results, map[string]any{"role": "tool", "tool_call_id": c.id, "content": result})
		case "responses":
			results = append(results, map[string]any{"type": "function_call_output", "call_id": c.id, "output": result})
		case "anthropic":
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": c.id, "content": result})
		case "gemini":
			results = append(results, map[string]any{"functionResponse": map[string]any{"id": c.id, "name": c.name, "response": map[string]any{"output": result}}})
		}
	}
	switch p.name {
	case "chat":
		p.body["messages"] = append(append(p.body["messages"].([]any), map[string]any{"role": "assistant", "tool_calls": invocation}), results...)
	case "responses":
		p.body["input"] = append(append([]any{map[string]any{"role": "user", "content": "hello"}}, invocation...), results...)
	case "anthropic":
		p.body["messages"] = append(p.body["messages"].([]any), map[string]any{"role": "assistant", "content": invocation}, map[string]any{"role": "user", "content": results})
	case "gemini":
		p.body["contents"] = append(p.body["contents"].([]any), map[string]any{"role": "model", "parts": invocation}, map[string]any{"role": "user", "parts": results})
	}
}
