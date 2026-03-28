package messages_test

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

var bookSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title":  map[string]any{"type": "string"},
		"author": map[string]any{"type": "string"},
		"year":   map[string]any{"type": "integer"},
	},
	"required": []string{"title", "author", "year"},
}

func TestStructuredOutputHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "Recommend a classic science fiction book. Respond with JSON only."},
				},
				"tools": []any{
					map[string]any{
						"name":         "book_recommendation",
						"description":  "Return a book recommendation",
						"input_schema": bookSchema,
					},
				},
				"tool_choice": map[string]any{
					"type": "tool",
					"name": "book_recommendation",
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireToolUseWithName(t, "anthropic", anthropicResp.Body, "book_recommendation")
			requireToolUseWithName(t, "wingman", wingmanResp.Body, "book_recommendation")

			requireValidBookFromToolUse(t, "anthropic", anthropicResp.Body)
			requireValidBookFromToolUse(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestStructuredOutputSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "Recommend a classic science fiction book."},
				},
				"tools": []any{
					map[string]any{
						"name":         "book_recommendation",
						"description":  "Return a book recommendation",
						"input_schema": bookSchema,
					},
				},
				"tool_choice": map[string]any{
					"type": "tool",
					"name": "book_recommendation",
				},
			}

			anthropicEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

			requireToolUseSSE(t, "anthropic", anthropicEvents)
			requireToolUseSSE(t, "wingman", wingmanEvents)
		})
	}
}

func requireValidBookFromToolUse(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] != "tool_use" {
			continue
		}

		input, ok := obj["input"].(map[string]any)
		if !ok {
			inputStr, ok := obj["input"].(string)
			if !ok {
				t.Fatalf("[%s] tool_use input is neither object nor string", label)
			}
			if err := json.Unmarshal([]byte(inputStr), &input); err != nil {
				t.Fatalf("[%s] tool_use input is not valid JSON: %v", label, err)
			}
		}

		if input["title"] == nil || input["title"] == "" {
			t.Errorf("[%s] book title is empty", label)
		}
		if input["author"] == nil || input["author"] == "" {
			t.Errorf("[%s] book author is empty", label)
		}

		return
	}

	t.Fatalf("[%s] no tool_use block found", label)
}
