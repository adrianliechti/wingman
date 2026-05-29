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

// TestStructuredOutputJsonSchemaHTTP exercises the newer
// generationConfig.responseJsonSchema field (a full JSON Schema) rather
// than the older responseSchema (OpenAPI-flavored). Wingman routes the
// two through different branches in handler_generate.go, so a test that
// only ever sets responseSchema leaves the responseJsonSchema branch
// dead code as far as e2e is concerned.
func TestStructuredOutputJsonSchemaHTTP(t *testing.T) {
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
					"responseJsonSchema": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
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

func TestStructuredOutputSSE(t *testing.T) {
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

			geminiEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

			requireValidBookJSONFromSSE(t, "gemini", geminiEvents)
			requireValidBookJSONFromSSE(t, "wingman", wingmanEvents)
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

// requireValidBookJSONFromSSE concatenates parts[0].text across all
// streamed chunks and asserts the result is the expected JSON object.
func requireValidBookJSONFromSSE(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	var text string
	for _, e := range events {
		if e.Data == nil {
			continue
		}
		candidates, _ := e.Data["candidates"].([]any)
		for _, c := range candidates {
			cand, _ := c.(map[string]any)
			content, _ := cand["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, p := range parts {
				part, _ := p.(map[string]any)
				if s, ok := part["text"].(string); ok {
					text += s
				}
			}
		}
	}

	if text == "" {
		t.Fatalf("[%s] no text accumulated across SSE events", label)
	}

	var book struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Year   int    `json:"year"`
	}
	if err := json.Unmarshal([]byte(text), &book); err != nil {
		t.Fatalf("[%s] accumulated SSE text is not valid JSON: %v\ntext: %s", label, err, text)
	}
	if book.Title == "" {
		t.Errorf("[%s] book title is empty in SSE response", label)
	}
	if book.Author == "" {
		t.Errorf("[%s] book author is empty in SSE response", label)
	}
}
