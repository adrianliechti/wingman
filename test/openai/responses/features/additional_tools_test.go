package features_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// TestAdditionalToolsHTTP reproduces Codex Responses Lite: tools and developer
// instructions are input items, while the request-level tools and instructions
// fields are omitted. OpenAI is the reference for acceptance and tool visibility.
func TestAdditionalToolsHTTP(t *testing.T) {
	h := openai.New(t)

	body := map[string]any{
		"input": []any{
			map[string]any{
				"type": "additional_tools",
				"role": "developer",
				"tools": []any{
					map[string]any{
						"type":        "function",
						"name":        "get_customer",
						"description": "Look up a customer by ID.",
						"parameters": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"customer_id": map[string]any{"type": "string"},
							},
							"required":             []string{"customer_id"},
							"additionalProperties": false,
						},
					},
				},
			},
			map[string]any{
				"type": "message",
				"role": "developer",
				"content": []any{
					map[string]any{"type": "input_text", "text": "Use the customer tool when asked to look up an ID."},
				},
			},
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "Look up customer cus_123."},
				},
			},
		},
	}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			requireFunctionCall(t, "openai", openaiResp.Body, "get_customer")
			requireFunctionCall(t, "wingman", wingmanResp.Body, "get_customer")
		})
	}
}
