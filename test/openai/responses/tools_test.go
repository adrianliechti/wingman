package responses_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

var weatherTool = map[string]any{
	"type":        "function",
	"name":        "get_weather",
	"description": "Get the current weather for a location",
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The city and country, e.g. 'London, UK'",
			},
		},
		"required": []string{"location"},
	},
}

func TestToolCallingHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "What's the weather in London?",
				"tools": []any{weatherTool},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			// Both should return function_call output items
			requireFunctionCall(t, "openai", openaiResp.Body, "get_weather")
			requireFunctionCall(t, "wingman", wingmanResp.Body, "get_weather")

			rules := openai.DefaultResponseRules()
			// Some models return extra output items (e.g. message + function_call)
			rules["output"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolCallingSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			openaiBody := withModel(map[string]any{
				"input":  "What's the weather in London?",
				"tools":  []any{weatherTool},
				"stream": true,
			}, h.ReferenceModel)

			wingmanBody := withModel(map[string]any{
				"input":  "What's the weather in London?",
				"tools":  []any{weatherTool},
				"stream": true,
			}, model.Name)

			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", openaiBody)
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", wingmanBody)
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			requireFunctionCallSSEEvent(t, "openai", openaiEvents, "get_weather")
			requireFunctionCallSSEEvent(t, "wingman", wingmanEvents, "get_weather")
		})
	}
}

func TestToolCallingMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			// Simulate a full tool calling round-trip:
			// 1. User asks about weather
			// 2. Assistant calls get_weather
			// 3. Tool returns result
			// 4. Assistant responds with final answer
			body := map[string]any{
				"tools": []any{weatherTool},
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "What's the weather in London?"},
						},
					},
					{
						"type":      "function_call",
						"id":        "fc_test123",
						"call_id":   "call_test123",
						"name":      "get_weather",
						"arguments": `{"location": "London, UK"}`,
					},
					{
						"type":    "function_call_output",
						"call_id": "call_test123",
						"output":  "Sunny, 22°C with light winds",
					},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			// Final response should be a message (not another tool call)
			requireMessageOutput(t, "openai", openaiResp.Body)
			requireMessageOutput(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolChoiceNoneHTTP(t *testing.T) {
	h := openai.New(t)

	tools := []any{weatherTool}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input":       "What is 2+2? Answer directly.",
				"tools":       tools,
				"tool_choice": "none",
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			requireMessageOutput(t, "openai", openaiResp.Body)
			requireMessageOutput(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// requireFunctionCall checks that the response contains a function_call with the given name.
func requireFunctionCall(t *testing.T, label string, body map[string]any, name string) {
	t.Helper()

	if hasFunctionCall(body, name) {
		return
	}

	t.Fatalf("[%s] no function_call output item with name %q found", label, name)
}

func hasFunctionCall(body map[string]any, name string) bool {
	output, ok := body["output"].([]any)
	if !ok {
		return false
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "function_call" && obj["name"] == name {
			args, _ := obj["arguments"].(string)
			if args != "" {
				var parsed map[string]any
				if json.Unmarshal([]byte(args), &parsed) == nil {
					return true
				}
			}
		}
	}

	return false
}

// requireAnyFunctionCall checks that the response contains at least one function_call.
func requireAnyFunctionCall(t *testing.T, label string, body map[string]any) {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "function_call" {
			return
		}
	}

	t.Fatalf("[%s] no function_call output item found", label)
}

// requireMessageOutput checks that the response contains a message output item.
func requireMessageOutput(t *testing.T, label string, body map[string]any) {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "message" {
			return
		}
	}

	t.Fatalf("[%s] no message output item found", label)
}

// requireFunctionCallSSEEvent checks that the SSE stream contains a function_call output_item.
func requireFunctionCallSSEEvent(t *testing.T, label string, events []*harness.SSEEvent, name string) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		itemType, _ := e.Data["type"].(string)
		if itemType != "response.output_item.added" {
			continue
		}

		item, ok := e.Data["item"].(map[string]any)
		if !ok {
			continue
		}

		if item["type"] == "function_call" && item["name"] == name {
			return
		}
	}

	t.Fatalf("[%s] no function_call SSE event with name %q found", label, name)
}
