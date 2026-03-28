package messages_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestComputerUseHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.ComputerUse {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "Take a screenshot"},
				},
				"tools": []any{
					map[string]any{
						"type":             "computer_20251124",
						"name":             "computer",
						"display_width_px": 1024,
						"display_height_px": 768,
					},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireComputerUseCall(t, "anthropic", anthropicResp.Body)
			requireComputerUseCall(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireComputerUseCall(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] == "tool_use" && obj["name"] == "computer" {
			return
		}
	}

	t.Fatalf("[%s] no computer tool_use block found", label)
}
