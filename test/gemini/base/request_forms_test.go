package base_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

// TestProtobufRequestFormsHTTP sends the request the way Google's own REST
// examples write it: snake_case field names and single objects where the
// schema has repeated fields. Both endpoints must accept it.
func TestProtobufRequestFormsHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"system_instruction": map[string]any{
					"parts": map[string]any{"text": "Answer with a single word."},
				},
				"contents": map[string]any{
					"role":  "user",
					"parts": map[string]any{"text": "What color is a ripe banana?"},
				},
				"generation_config": map[string]any{
					"max_output_tokens": 200,
					"stop_sequences":    "zzz",
				},
			}

			geminiResp, wingmanResp := gemini.CompareHTTP(t, h, model.Name, body)

			gemini.RequireTextResponse(t, "gemini", geminiResp.Body)
			gemini.RequireTextResponse(t, "wingman", wingmanResp.Body)

			rules := gemini.DefaultResponseRules()
			harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// TestProtobufToolFormsHTTP uses the snake_case tool declaration form with a
// singleton function_declarations object and a forced call.
func TestProtobufToolFormsHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": map[string]any{
					"role":  "user",
					"parts": map[string]any{"text": "What's the weather in London?"},
				},
				"tools": map[string]any{
					"function_declarations": map[string]any{
						"name":        "get_weather",
						"description": "Get the current weather for a location",
						"parameters": map[string]any{
							"type":       "object",
							"properties": map[string]any{"location": map[string]any{"type": "string"}},
							"required":   []string{"location"},
						},
					},
				},
				"tool_config": map[string]any{
					"function_calling_config": map[string]any{"mode": "ANY", "allowed_function_names": "get_weather"},
				},
			}

			geminiResp, wingmanResp := gemini.CompareHTTP(t, h, model.Name, body)

			requireFunctionCallWithName(t, "gemini", geminiResp.Body, "get_weather")
			requireFunctionCallWithName(t, "wingman", wingmanResp.Body, "get_weather")
		})
	}
}
