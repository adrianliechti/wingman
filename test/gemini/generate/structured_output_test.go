package generate_test

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestStructuredOutputHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		if !model.Capabilities.StructuredOutput {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "Recommend a classic science fiction book."}}},
				},
				"generationConfig": map[string]any{
					"responseMimeType": "application/json",
					"responseSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":  map[string]any{"type": "string"},
							"author": map[string]any{"type": "string"},
							"year":   map[string]any{"type": "integer"},
						},
						"required": []string{"title", "author", "year"},
					},
				},
			}

			geminiResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireValidBookJSON(t, "gemini", geminiResp.Body)
			requireValidBookJSON(t, "wingman", wingmanResp.Body)

			rules := gemini.DefaultResponseRules()
			harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireValidBookJSON(t *testing.T, label string, body map[string]any) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("[%s] no candidates", label)
	}

	cand, _ := candidates[0].(map[string]any)
	content, _ := cand["content"].(map[string]any)
	parts, _ := content["parts"].([]any)

	if len(parts) == 0 {
		t.Fatalf("[%s] no parts", label)
	}

	part, _ := parts[0].(map[string]any)
	text, _ := part["text"].(string)

	var book struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Year   int    `json:"year"`
	}

	if err := json.Unmarshal([]byte(text), &book); err != nil {
		t.Fatalf("[%s] not valid JSON: %v\ntext: %s", label, err, text)
	}

	if book.Title == "" {
		t.Errorf("[%s] title is empty", label)
	}
	if book.Author == "" {
		t.Errorf("[%s] author is empty", label)
	}
}
