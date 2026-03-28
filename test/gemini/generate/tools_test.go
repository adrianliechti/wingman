package generate_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

var weatherTool = map[string]any{
	"functionDeclarations": []map[string]any{
		{
			"name":        "get_weather",
			"description": "Get the current weather for a location",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "The city and country",
					},
				},
				"required": []string{"location"},
			},
		},
	},
}

func TestToolCallingHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "What's the weather in London?"}}},
				},
				"tools": []any{weatherTool},
			}

			geminiResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireFunctionCallWithName(t, "gemini", geminiResp.Body, "get_weather")
			requireFunctionCallWithName(t, "wingman", wingmanResp.Body, "get_weather")

			rules := gemini.DefaultResponseRules()
			rules["candidates.*.content.parts.*.functionCall.args"] = harness.FieldPresence
			rules["candidates.*.content.parts.*.functionCall.id"] = harness.FieldIgnore
			harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// TestToolCallingMultiTurnHTTP tests multi-turn tool calling.
// Note: Gemini 3 requires thought_signature on functionCall parts which makes
// synthetic multi-turn tests impractical. Skipped for now.
func TestToolCallingMultiTurnHTTP(t *testing.T) {
	t.Skip("Gemini 3 requires thought_signature on functionCall parts")

	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "What's the weather in London?"}}},
					{"role": "model", "parts": []map[string]any{
						{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"location": "London"}}},
					}},
					{"role": "user", "parts": []map[string]any{
						{"functionResponse": map[string]any{"id": "call_test", "name": "get_weather", "response": map[string]any{"result": "Sunny, 22°C"}}},
					}},
				},
				"tools": []any{weatherTool},
			}

			geminiResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireTextResponse(t, "gemini", geminiResp.Body)
			requireTextResponse(t, "wingman", wingmanResp.Body)

			rules := gemini.DefaultResponseRules()
			harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireFunctionCall(t *testing.T, label string, body map[string]any) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	for _, c := range candidates {
		cand, _ := c.(map[string]any)
		content, _ := cand["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if _, ok := part["functionCall"]; ok {
				return
			}
		}
	}

	t.Fatalf("[%s] no functionCall found in response", label)
}

func requireFunctionCallWithName(t *testing.T, label string, body map[string]any, name string) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	for _, c := range candidates {
		cand, _ := c.(map[string]any)
		content, _ := cand["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			fc, _ := part["functionCall"].(map[string]any)
			if fc["name"] == name {
				return
			}
		}
	}

	t.Fatalf("[%s] no functionCall with name %q found", label, name)
}

func requireTextResponse(t *testing.T, label string, body map[string]any) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	for _, c := range candidates {
		cand, _ := c.(map[string]any)
		content, _ := cand["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if text, ok := part["text"].(string); ok && text != "" {
				return
			}
		}
	}

	t.Fatalf("[%s] no text response found", label)
}
