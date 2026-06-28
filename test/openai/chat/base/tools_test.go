package base_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/chat"
)

var weatherTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "get_weather",
		"description": "Get the current weather for a location",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "The city and country",
				},
			},
			"required": []string{"location"},
		},
	},
}

func TestToolCallingHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}

			openaiResp, wingmanResp := chat.CompareHTTP(t, h, model, body)

			requireToolCall(t, "openai", openaiResp.Body, "get_weather")
			requireToolCall(t, "wingman", wingmanResp.Body, "get_weather")

			rules := openai.DefaultChatResponseRules()
			rules["choices.*.message.tool_calls.*.id"] = harness.FieldPresence
			rules["choices.*.message.tool_calls.*.function.arguments"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolCallingSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			openaiBody := chat.WithModel(map[string]any{
				"stream": true,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}, h.ReferenceModel)

			wingmanBody := chat.WithModel(map[string]any{
				"stream": true,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}, model.Name)

			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/chat/completions", openaiBody)
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/chat/completions", wingmanBody)
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			requireToolCallSSE(t, "openai", openaiEvents)
			requireToolCallSSE(t, "wingman", wingmanEvents)
		})
	}
}

func TestToolCallingMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
					{
						"role": "assistant",
						"tool_calls": []map[string]any{
							{
								"id":   "call_test123",
								"type": "function",
								"function": map[string]any{
									"name":      "get_weather",
									"arguments": `{"location": "London, UK"}`,
								},
							},
						},
					},
					{
						"role":         "tool",
						"tool_call_id": "call_test123",
						"content":      "Sunny, 22°C with light winds",
					},
				},
				"tools": []any{weatherTool},
			}

			openaiResp, wingmanResp := chat.CompareHTTP(t, h, model, body)

			// Final response should have content (not another tool call)
			requireContent(t, "openai", openaiResp.Body)
			requireContent(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultChatResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolChoiceNoneHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "What is 2+2? Answer directly."},
				},
				"tools":       []any{weatherTool},
				"tool_choice": "none",
			}

			openaiResp, wingmanResp := chat.CompareHTTP(t, h, model, body)

			requireContent(t, "openai", openaiResp.Body)
			requireContent(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultChatResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireToolCall(t *testing.T, label string, body map[string]any, name string) {
	t.Helper()

	choices, ok := body["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("[%s] no choices in response", label)
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("[%s] choice is not an object", label)
	}

	msg, ok := choice["message"].(map[string]any)
	if !ok {
		t.Fatalf("[%s] message is not an object", label)
	}

	toolCalls, ok := msg["tool_calls"].([]any)
	if !ok || len(toolCalls) == 0 {
		t.Fatalf("[%s] no tool_calls in message", label)
	}

	for _, tc := range toolCalls {
		call, ok := tc.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := call["function"].(map[string]any)
		if !ok {
			continue
		}
		if fn["name"] == name {
			args, _ := fn["arguments"].(string)
			var parsed map[string]any
			if json.Unmarshal([]byte(args), &parsed) == nil {
				return
			}
		}
	}

	t.Fatalf("[%s] no tool_call with name %q found", label, name)
}

func requireToolCallSSE(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		choices, ok := e.Data["choices"].([]any)
		if !ok {
			continue
		}

		for _, c := range choices {
			choice, ok := c.(map[string]any)
			if !ok {
				continue
			}
			delta, ok := choice["delta"].(map[string]any)
			if !ok {
				continue
			}
			if toolCalls, ok := delta["tool_calls"].([]any); ok && len(toolCalls) > 0 {
				return
			}
		}
	}

	t.Fatalf("[%s] no tool_call SSE event found", label)
}

// longNoteTool has a single free-text argument. Forcing the model to fill it
// with a large value makes the tool-call arguments span many streamed deltas,
// which exercises (and guards against) truncated/dropped-chunk reassembly.
var longNoteTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "save_note",
		"description": "Persist a note containing arbitrary text",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "The full, verbatim text of the note",
				},
			},
			"required": []string{"content"},
		},
	},
}

// Varied original prose (not repetition, which can trip output content filters)
// reliably yields a long argument that spans many streamed deltas.
const longNotePrompt = "Call save_note exactly once. Set its content argument to a vivid, original " +
	"description of an imaginary coastal city — at least 200 words of flowing prose. " +
	"Put the full description directly in the content value."

func TestToolCallingLongArgumentsSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			req := map[string]any{
				"stream": true,
				"messages": []map[string]any{
					{"role": "user", "content": longNotePrompt},
				},
				"tools": []any{longNoteTool},
				"tool_choice": map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "save_note"},
				},
			}

			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/chat/completions", chat.WithModel(req, h.ReferenceModel))
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/chat/completions", chat.WithModel(req, model.Name))
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			requireLongToolArgumentsChat(t, "openai", openaiEvents)
			requireLongToolArgumentsChat(t, "wingman", wingmanEvents)
		})
	}
}

// requireLongToolArgumentsChat concatenates the streamed tool-call argument
// fragments (keyed by index, per the OpenAI streaming contract) and asserts the
// result is complete, valid JSON with a long value.
func requireLongToolArgumentsChat(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	args := map[int]string{}
	var order []int

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		choices, _ := e.Data["choices"].([]any)
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if delta == nil {
				continue
			}

			toolCalls, _ := delta["tool_calls"].([]any)
			for _, tc := range toolCalls {
				call, _ := tc.(map[string]any)

				idx := 0
				if f, ok := call["index"].(float64); ok {
					idx = int(f)
				}

				fn, _ := call["function"].(map[string]any)
				if fn == nil {
					continue
				}

				if _, seen := args[idx]; !seen {
					order = append(order, idx)
				}

				if a, ok := fn["arguments"].(string); ok {
					args[idx] += a
				}
			}
		}
	}

	if len(order) == 0 {
		t.Fatalf("[%s] no tool_call deltas found in stream", label)
	}

	requireLongJSONArguments(t, label, args[order[0]])
}

// requireLongJSONArguments asserts that a reassembled tool-call argument string
// is complete, valid JSON carrying a long "content" value. A truncated stream
// (a dropped or partial chunk) fails the json.Unmarshal here.
func requireLongJSONArguments(t *testing.T, label, args string) {
	t.Helper()

	if args == "" {
		t.Fatalf("[%s] reassembled tool arguments are empty", label)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Fatalf("[%s] reassembled tool arguments are not valid JSON (likely a truncated/dropped stream chunk): %v\n  length=%d\n  tail=%q", label, err, len(args), argsTail(args))
	}

	content, ok := parsed["content"].(string)
	if !ok {
		t.Fatalf("[%s] reassembled arguments missing string 'content' field: %s", label, args)
	}

	if len(content) < 400 {
		t.Fatalf("[%s] expected a long 'content' spanning multiple stream chunks, got %d chars", label, len(content))
	}
}

func argsTail(s string) string {
	const n = 80
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func requireContent(t *testing.T, label string, body map[string]any) {
	t.Helper()

	choices, ok := body["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("[%s] no choices in response", label)
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("[%s] choice is not an object", label)
	}

	msg, ok := choice["message"].(map[string]any)
	if !ok {
		t.Fatalf("[%s] message is not an object", label)
	}

	content, _ := msg["content"].(string)
	if content == "" {
		t.Fatalf("[%s] message content is empty", label)
	}
}
