package features_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

// TestToolSearchHTTP registers the native tool search tool plus a deferred
// function tool. The model must discover get_weather through search and call
// it. Wingman runs the search transparently inside the turn, so only the
// discovered tool's tool_use is asserted on both sides.
func TestToolSearchHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.ToolSearch {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "What is the weather in San Francisco? Use the available tools."},
				},
				"tools": []any{
					map[string]any{
						"type": "tool_search_tool_regex_20251119",
						"name": "tool_search_tool_regex",
					},
					map[string]any{
						"name":        "get_time",
						"description": "Get the current time.",
						"input_schema": map[string]any{
							"type":       "object",
							"properties": map[string]any{},
						},
					},
					map[string]any{
						"name":        "get_weather",
						"description": "Get the current weather for a location.",
						"input_schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"location": map[string]any{"type": "string"},
							},
							"required": []string{"location"},
						},
						"defer_loading": true,
					},
				},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			requireToolUse(t, "anthropic", anthropicResp.Body, "get_weather")
			requireToolUse(t, "wingman", wingmanResp.Body, "get_weather")
		})
	}
}

func requireToolUse(t *testing.T, label string, body map[string]any, name string) {
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

	t.Fatalf("[%s] no tool_use named %q found", label, name)
}
