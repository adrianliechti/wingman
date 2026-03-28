package messages_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

var weatherTool = map[string]any{
	"name":        "get_weather",
	"description": "Get the current weather for a location",
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The city and country",
			},
		},
		"required": []string{"location"},
	},
}

func TestToolCallingHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model, body)

			requireToolUseWithName(t, "anthropic", anthropicResp.Body, "get_weather")
			requireToolUseWithName(t, "wingman", wingmanResp.Body, "get_weather")

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolCallingSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model, func(t *testing.T) {
			anthropicBody := withModel(map[string]any{
				"max_tokens": 1024,
				"stream":     true,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}, h.ReferenceModel)

			wingmanBody := withModel(map[string]any{
				"max_tokens": 1024,
				"stream":     true,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}, model)

			anthropicEvents := postAnthropicSSE(t, h, h.Anthropic, anthropicBody)
			wingmanEvents := postAnthropicSSE(t, h, h.Wingman, wingmanBody)

			requireToolUseSSE(t, "anthropic", anthropicEvents)
			requireToolUseSSE(t, "wingman", wingmanEvents)
		})
	}
}

func TestToolCallingMultiTurnHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
					{
						"role": "assistant",
						"content": []map[string]any{
							{
								"type":  "tool_use",
								"id":    "toolu_test123",
								"name":  "get_weather",
								"input": map[string]any{"location": "London, UK"},
							},
						},
					},
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type":        "tool_result",
								"tool_use_id": "toolu_test123",
								"content":     "Sunny, 22°C with light winds",
							},
						},
					},
				},
				"tools": []any{weatherTool},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model, body)

			requireTextContent(t, "anthropic", anthropicResp.Body)
			requireTextContent(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireToolUseWithName(t *testing.T, label string, body map[string]any, name string) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	for _, block := range content {
		obj, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "tool_use" && obj["name"] == name {
			return
		}
	}

	t.Fatalf("[%s] no tool_use block with name %q found", label, name)
}

func requireTextContent(t *testing.T, label string, body map[string]any) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	for _, block := range content {
		obj, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "text" {
			text, _ := obj["text"].(string)
			if text != "" {
				return
			}
		}
	}

	t.Fatalf("[%s] no text content block found", label)
}

func requireToolUseSSE(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		eventType, _ := e.Data["type"].(string)
		if eventType != "content_block_start" {
			continue
		}

		block, ok := e.Data["content_block"].(map[string]any)
		if !ok {
			continue
		}

		if block["type"] == "tool_use" {
			return
		}
	}

	t.Fatalf("[%s] no tool_use SSE event found", label)
}
