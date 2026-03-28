package generate_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestBasicHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			tests := []struct {
				name string
				body map[string]any
			}{
				{
					name: "simple message",
					body: map[string]any{
						"contents": []map[string]any{
							{"role": "user", "parts": []map[string]any{{"text": "Say hello and nothing else."}}},
						},
					},
				},
				{
					name: "with system instruction",
					body: map[string]any{
						"systemInstruction": map[string]any{
							"parts": []map[string]any{{"text": "You are a helpful assistant. Always respond in exactly one word."}},
						},
						"contents": []map[string]any{
							{"role": "user", "parts": []map[string]any{{"text": "What is the capital of France?"}}},
						},
					},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					geminiResp, wingmanResp := compareHTTP(t, h, model.Name, tt.body)

					rules := gemini.DefaultResponseRules()
					harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func TestBasicSSE(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "Say hello and nothing else."}}},
				},
			}

			geminiEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

			rules := gemini.DefaultSSERules()
			harness.CompareSSEStructureByType(t, geminiEvents, wingmanEvents, rules)
		})
	}
}
