package messages_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestTextEditorCreateHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.TextEditor {
			continue
		}
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "Create a file called hello.py with print(\"Hello, world!\")"},
				},
				"tools": []any{
					map[string]any{
						"type": "text_editor_20250728",
						"name": "str_replace_based_edit_tool",
					},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireTextEditorCall(t, "anthropic", anthropicResp.Body, "create")
			requireTextEditorCall(t, "wingman", wingmanResp.Body, "create")

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestTextEditorEditHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.TextEditor {
			continue
		}
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "Create a file called hello.py with print(\"Hello, world!\")"},
					{
						"role": "assistant",
						"content": []map[string]any{
							{
								"type": "tool_use",
								"id":   "toolu_test123",
								"name": "str_replace_based_edit_tool",
								"input": map[string]any{
									"command":   "create",
									"path":      "hello.py",
									"file_text": "print(\"Hello, world!\")\n",
								},
							},
						},
					},
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type":        "tool_result",
								"tool_use_id": "toolu_test123",
								"content":     "File created successfully.",
							},
						},
					},
					{"role": "user", "content": "Now change the message to Goodbye"},
				},
				"tools": []any{
					map[string]any{
						"type": "text_editor_20250728",
						"name": "str_replace_based_edit_tool",
					},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireTextEditorCall(t, "anthropic", anthropicResp.Body, "str_replace")
			requireTextEditorCall(t, "wingman", wingmanResp.Body, "str_replace")

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireTextEditorCall(t *testing.T, label string, body map[string]any, command string) {
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
		if obj["type"] != "tool_use" || obj["name"] != "str_replace_based_edit_tool" {
			continue
		}

		input, ok := obj["input"].(map[string]any)
		if !ok {
			continue
		}

		if input["command"] == command {
			return
		}
	}

	t.Fatalf("[%s] no text_editor tool_use with command %q found", label, command)
}
