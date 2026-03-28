package chat_test

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

var bookSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title":  map[string]any{"type": "string"},
		"author": map[string]any{"type": "string"},
		"year":   map[string]any{"type": "integer"},
	},
	"required":             []string{"title", "author", "year"},
	"additionalProperties": false,
}

func TestChatStructuredOutputHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "Recommend a classic science fiction book. Respond with JSON only."},
				},
				"response_format": map[string]any{
					"type": "json_schema",
					"json_schema": map[string]any{
						"name":   "book_recommendation",
						"schema": bookSchema,
						"strict": true,
					},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			requireValidBookJSON(t, "openai", openaiResp.Body)
			requireValidBookJSON(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultChatResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestChatStructuredOutputSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "Recommend a classic science fiction book. Respond with JSON only."},
				},
				"response_format": map[string]any{
					"type": "json_schema",
					"json_schema": map[string]any{
						"name":   "book_recommendation",
						"schema": bookSchema,
						"strict": true,
					},
				},
			}

			openaiEvents, wingmanEvents := compareSSE(t, h, model, body)

			rules := openai.DefaultChatSSEEventRules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
		})
	}
}

func TestChatJSONObjectFormatHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "system", "content": "You are a helpful assistant that responds only in valid JSON format."},
					{"role": "user", "content": `Respond with exactly this JSON object: {"answer": 42}`},
				},
				"response_format": map[string]any{
					"type": "json_object",
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			requireValidJSON(t, "openai", openaiResp.Body)
			requireValidJSON(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultChatResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireValidBookJSON(t *testing.T, label string, body map[string]any) {
	t.Helper()

	text := extractContent(t, label, body)

	var book struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Year   int    `json:"year"`
	}

	if err := json.Unmarshal([]byte(text), &book); err != nil {
		t.Fatalf("[%s] output is not valid JSON: %v\ntext: %s", label, err, text)
	}

	if book.Title == "" {
		t.Errorf("[%s] book title is empty", label)
	}
	if book.Author == "" {
		t.Errorf("[%s] book author is empty", label)
	}
	if book.Year == 0 {
		t.Errorf("[%s] book year is zero", label)
	}
}

func requireValidJSON(t *testing.T, label string, body map[string]any) {
	t.Helper()

	text := extractContent(t, label, body)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("[%s] output is not valid JSON: %v\ntext: %s", label, err, text)
	}
}

func extractContent(t *testing.T, label string, body map[string]any) string {
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

	return content
}
