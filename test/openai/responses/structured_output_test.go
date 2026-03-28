package responses_test

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
		"genres": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
	},
	"required":             []string{"title", "author", "year", "genres"},
	"additionalProperties": false,
}

func TestStructuredOutputHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "Recommend a classic science fiction book. Respond with JSON only.",
				"text": map[string]any{
					"format": map[string]any{
						"type":   "json_schema",
						"name":   "book_recommendation",
						"schema": bookSchema,
						"strict": true,
					},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			// Validate both responses are valid JSON matching the schema
			requireValidBookJSON(t, "openai", openaiResp.Body)
			requireValidBookJSON(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestStructuredOutputSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "Recommend a classic science fiction book. Respond with JSON only.",
				"text": map[string]any{
					"format": map[string]any{
						"type":   "json_schema",
						"name":   "book_recommendation",
						"schema": bookSchema,
						"strict": true,
					},
				},
			}

			openaiEvents, wingmanEvents := compareSSE(t, h, model, body)

			rules := openai.DefaultResponsesSSERules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)

			// Validate the completed response contains valid JSON
			requireValidBookJSONFromSSE(t, "openai", openaiEvents)
			requireValidBookJSONFromSSE(t, "wingman", wingmanEvents)
		})
	}
}

func TestJSONObjectFormatHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"instructions": "You are a helpful assistant that responds only in valid JSON format.",
				"input":        `Respond with exactly this JSON object: {"answer": 42}`,
				"text": map[string]any{
					"format": map[string]any{
						"type": "json_object",
					},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			// Validate both responses are valid JSON
			requireValidJSON(t, "openai", openaiResp.Body)
			requireValidJSON(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireValidBookJSON(t *testing.T, label string, body map[string]any) {
	t.Helper()

	text := extractOutputText(t, label, body)

	var book struct {
		Title  string   `json:"title"`
		Author string   `json:"author"`
		Year   int      `json:"year"`
		Genres []string `json:"genres"`
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
	if len(book.Genres) == 0 {
		t.Errorf("[%s] book genres is empty", label)
	}
}

func requireValidJSON(t *testing.T, label string, body map[string]any) {
	t.Helper()

	text := extractOutputText(t, label, body)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("[%s] output is not valid JSON: %v\ntext: %s", label, err, text)
	}
}

func extractOutputText(t *testing.T, label string, body map[string]any) string {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] != "message" {
			continue
		}

		content, ok := obj["content"].([]any)
		if !ok {
			continue
		}

		for _, part := range content {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := p["text"].(string); ok && text != "" {
				return text
			}
		}
	}

	t.Fatalf("[%s] no text found in message output", label)
	return ""
}

func requireValidBookJSONFromSSE(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	// Find the response.completed event and extract the output text
	for _, e := range events {
		if e.Data == nil {
			continue
		}

		eventType, _ := e.Data["type"].(string)
		if eventType != "response.completed" {
			continue
		}

		resp, ok := e.Data["response"].(map[string]any)
		if !ok {
			continue
		}

		text := extractOutputText(t, label, resp)

		var book struct {
			Title  string   `json:"title"`
			Author string   `json:"author"`
			Year   int      `json:"year"`
			Genres []string `json:"genres"`
		}

		if err := json.Unmarshal([]byte(text), &book); err != nil {
			t.Fatalf("[%s] SSE completed response is not valid JSON: %v\ntext: %s", label, err, text)
		}

		if book.Title == "" {
			t.Errorf("[%s] book title is empty in SSE response", label)
		}

		return
	}

	t.Fatalf("[%s] no response.completed SSE event found", label)
}
