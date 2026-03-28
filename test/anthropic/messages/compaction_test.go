package messages_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func buildAnthropicCompactionInput() []map[string]any {
	padding := strings.Repeat("This is filler text to increase the token count of this conversation so that compaction triggers. ", 3000)

	return []map[string]any{
		{"role": "user", "content": "Remember: the secret code is ALPHA-7. " + padding},
		{"role": "assistant", "content": "I will remember the code ALPHA-7. " + padding},
		{"role": "user", "content": "What is 2+2? Reply with just the number."},
	}
}

func TestCompactionHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Compaction {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages":   buildAnthropicCompactionInput(),
				"context_management": map[string]any{
					"edits": []map[string]any{
						{
							"type": "compact_20260112",
							"trigger": map[string]any{
								"type":  "input_tokens",
								"value": 50000,
							},
						},
					},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireCompactionBlock(t, "anthropic", anthropicResp.Body)
			requireCompactionBlock(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.content"] = harness.FieldIgnore
			rules["context_management"] = harness.FieldIgnore
			rules["usage.iterations"] = harness.FieldIgnore
			rules["usage.server_tool_use"] = harness.FieldIgnore
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireCompactionBlock(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] == "compaction" {
			c, _ := obj["content"].(string)
			if c == "" {
				t.Errorf("[%s] compaction block has empty content", label)
			}
			return
		}
	}

	t.Fatalf("[%s] no compaction content block found", label)
}
