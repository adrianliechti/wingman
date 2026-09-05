package base_test

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

var recipeSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name":     map[string]any{"type": "string"},
		"servings": map[string]any{"type": "integer"},
	},
	"required":             []string{"name", "servings"},
	"additionalProperties": false,
}

// TestOutputConfigFormatHTTP exercises structured output via the canonical
// output_config.format parameter (what current SDKs send) instead of the
// deprecated top-level output_format.
func TestOutputConfigFormatHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.StructuredOutput {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "Suggest a simple pasta recipe."},
				},
				"output_config": map[string]any{
					"format": map[string]any{
						"type":   "json_schema",
						"schema": recipeSchema,
					},
				},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			requireValidRecipeText(t, "anthropic", anthropicResp.Body)
			requireValidRecipeText(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireValidRecipeText(t *testing.T, label string, body map[string]any) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	for _, block := range content {
		obj, _ := block.(map[string]any)
		if obj["type"] != "text" {
			continue
		}

		text, _ := obj["text"].(string)
		if text == "" {
			continue
		}

		var recipe struct {
			Name     string `json:"name"`
			Servings int    `json:"servings"`
		}

		if err := json.Unmarshal([]byte(text), &recipe); err != nil {
			t.Fatalf("[%s] text is not valid JSON: %v\ntext: %s", label, err, text)
		}

		if recipe.Name == "" {
			t.Errorf("[%s] recipe name is empty", label)
		}
		if recipe.Servings <= 0 {
			t.Errorf("[%s] recipe servings is %d", label, recipe.Servings)
		}

		return
	}

	t.Fatalf("[%s] no text content block found", label)
}

// TestOutputConfigFormatWithToolsHTTP combines structured output with a
// client tool. The Messages API supports both at once: the model still emits
// a tool_use block, and only its final text answer is schema-constrained.
func TestOutputConfigFormatWithToolsHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.StructuredOutput {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London? Use the tool."},
				},
				"tools": []any{anthropic.WeatherTool},
				"output_config": map[string]any{
					"format": map[string]any{
						"type":   "json_schema",
						"schema": weatherReportSchema,
					},
				},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			requireToolUseWithName(t, "anthropic", anthropicResp.Body, "get_weather")
			requireToolUseWithName(t, "wingman", wingmanResp.Body, "get_weather")

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestOutputConfigFormatWithToolsSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.StructuredOutput {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London? Use the tool."},
				},
				"tools": []any{anthropic.WeatherTool},
				"output_config": map[string]any{
					"format": map[string]any{
						"type":   "json_schema",
						"schema": weatherReportSchema,
					},
				},
			}

			anthropicEvents, wingmanEvents := anthropic.CompareSSE(t, h, model.Name, body)

			requireToolUseSSE(t, "anthropic", anthropicEvents)
			requireToolUseSSE(t, "wingman", wingmanEvents)
		})
	}
}

var weatherReportSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary":       map[string]any{"type": "string"},
		"temperature_c": map[string]any{"type": "number"},
	},
	"required":             []string{"summary", "temperature_c"},
	"additionalProperties": false,
}
