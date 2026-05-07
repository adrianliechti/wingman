package messages_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

// TestDocumentTextSourceHTTP sends a plain-text document block (the simplest
// document source type — no fixture required) and asserts the model can
// answer about it. This exercises the input parsing path for `document`
// content blocks; without it the document is silently dropped and the
// model has no context to answer from.
func TestDocumentTextSourceHTTP(t *testing.T) {
	h := anthropic.New(t)

	docText := "The lab notebook records that compound BANANA-RHUBARB-7 was synthesized on Tuesday."

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 200,
				"messages": []map[string]any{
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type": "document",
								"source": map[string]any{
									"type":       "text",
									"media_type": "text/plain",
									"data":       docText,
								},
							},
							{
								"type": "text",
								"text": "Which compound is mentioned in the document? Reply with just the compound identifier.",
							},
						},
					},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireDocumentAnswered(t, "anthropic", anthropicResp.Body, "BANANA-RHUBARB-7")
			requireDocumentAnswered(t, "wingman", wingmanResp.Body, "BANANA-RHUBARB-7")

			rules := anthropic.DefaultMessagesResponseRules()
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireDocumentAnswered(t *testing.T, label string, body map[string]any, needle string) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	for _, block := range content {
		obj, _ := block.(map[string]any)
		if obj["type"] != "text" {
			continue
		}
		if text, _ := obj["text"].(string); text != "" && containsCaseInsensitive(text, needle) {
			return
		}
	}

	t.Fatalf("[%s] expected response to contain %q, got: %v", label, needle, content)
}

func containsCaseInsensitive(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := haystack[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
