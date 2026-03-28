package generate_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestThinkingHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "I have 3 apples and give away 1. Then I buy 5 more and give away 2. Then I eat 1. My friend gives me 3 and I give her back 2. How many apples do I have? Show your reasoning step by step."}}},
				},
				"generationConfig": map[string]any{
					"thinkingConfig": map[string]any{
						"includeThoughts": true,
					},
				},
			}

			geminiResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireThoughtPart(t, "gemini", geminiResp.Body)
			requireThoughtPart(t, "wingman", wingmanResp.Body)

			rules := gemini.DefaultResponseRules()
			rules["candidates.*.content.parts"] = harness.FieldPresence
			harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireThoughtPart(t *testing.T, label string, body map[string]any) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	for _, c := range candidates {
		cand, _ := c.(map[string]any)
		content, _ := cand["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if thought, ok := part["thought"].(bool); ok && thought {
				return
			}
		}
	}

	t.Fatalf("[%s] no thought part found in response", label)
}
