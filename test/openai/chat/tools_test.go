package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
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

func TestChatToolCallingHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			requireToolCall(t, "openai", openaiResp.Body, "get_weather")
			requireToolCall(t, "wingman", wingmanResp.Body, "get_weather")

			rules := openai.DefaultChatResponseRules()
			rules["choices.*.message.tool_calls.*.id"] = harness.FieldPresence
			rules["choices.*.message.tool_calls.*.function.arguments"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestChatToolCallingSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			openaiBody := withModel(map[string]any{
				"stream": true,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}, h.ReferenceModel)

			wingmanBody := withModel(map[string]any{
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

func TestChatToolCallingMultiTurnHTTP(t *testing.T) {
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

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			// Final response should have content (not another tool call)
			requireContent(t, "openai", openaiResp.Body)
			requireContent(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultChatResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestChatToolChoiceNoneHTTP(t *testing.T) {
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

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

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
