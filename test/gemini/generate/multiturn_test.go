package generate_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestMultiTurnHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "My name is Alice."}}},
					{"role": "model", "parts": []map[string]any{{"text": "Nice to meet you, Alice!"}}},
					{"role": "user", "parts": []map[string]any{{"text": "What is my name? Reply with just the name."}}},
				},
			}

			geminiResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			rules := gemini.DefaultResponseRules()
			harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestMultiTurnSSE(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "My name is Alice."}}},
					{"role": "model", "parts": []map[string]any{{"text": "Nice to meet you, Alice!"}}},
					{"role": "user", "parts": []map[string]any{{"text": "What is my name? Reply with just the name."}}},
				},
			}

			geminiEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

			rules := gemini.DefaultSSERules()
			harness.CompareSSEStructureByType(t, geminiEvents, wingmanEvents, rules)
		})
	}
}
