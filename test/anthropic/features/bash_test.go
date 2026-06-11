package features_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestBashHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Shell {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "Use the bash tool to list the files in the current directory."},
				},
				"tools": []any{
					map[string]any{
						"type": "bash_20250124",
						"name": "bash",
					},
				},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			requireBashCall(t, "anthropic", anthropicResp.Body)
			requireBashCall(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestBashMultiTurnHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Shell {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "Use the bash tool to find out which Go version is installed, then summarize it."},
					{
						"role": "assistant",
						"content": []map[string]any{
							{
								"type": "tool_use",
								"id":   "toolu_bash_1",
								"name": "bash",
								"input": map[string]any{
									"command": "go version",
								},
							},
						},
					},
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type":        "tool_result",
								"tool_use_id": "toolu_bash_1",
								"content":     "go version go1.26.0 darwin/arm64",
							},
						},
					},
				},
				"tools": []any{
					map[string]any{
						"type": "bash_20250124",
						"name": "bash",
					},
				},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			requireTextAnswer(t, "anthropic", anthropicResp.Body)
			requireTextAnswer(t, "wingman", wingmanResp.Body)
		})
	}
}

func requireBashCall(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] != "tool_use" || obj["name"] != "bash" {
			continue
		}

		input, ok := obj["input"].(map[string]any)
		if !ok {
			continue
		}

		if command, _ := input["command"].(string); command != "" {
			return
		}
	}

	t.Fatalf("[%s] no bash tool_use with a command found", label)
}

func requireTextAnswer(t *testing.T, label string, body map[string]any) {
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

		if text, _ := obj["text"].(string); obj["type"] == "text" && text != "" {
			return
		}
	}

	t.Fatalf("[%s] no text answer found", label)
}
