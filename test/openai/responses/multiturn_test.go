package responses_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			tests := []struct {
				name string
				body map[string]any
			}{
				{
					name: "user assistant user",
					body: map[string]any{
						"input": []map[string]any{
							{
								"type": "message",
								"role": "user",
								"content": []map[string]any{
									{"type": "input_text", "text": "My name is Alice."},
								},
							},
							{
								"type": "message",
								"role": "assistant",
								"content": []map[string]any{
									{"type": "output_text", "text": "Nice to meet you, Alice!"},
								},
							},
							{
								"type": "message",
								"role": "user",
								"content": []map[string]any{
									{"type": "input_text", "text": "What is my name? Reply with just the name."},
								},
							},
						},
					},
				},
				{
					name: "with system instructions",
					body: map[string]any{
						"instructions": "You are a helpful assistant. Always respond in exactly one word.",
						"input": []map[string]any{
							{
								"type": "message",
								"role": "user",
								"content": []map[string]any{
									{"type": "input_text", "text": "The capital of France is?"},
								},
							},
							{
								"type": "message",
								"role": "assistant",
								"content": []map[string]any{
									{"type": "output_text", "text": "Paris"},
								},
							},
							{
								"type": "message",
								"role": "user",
								"content": []map[string]any{
									{"type": "input_text", "text": "And of Germany?"},
								},
							},
						},
					},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					openaiResp, wingmanResp := compareHTTP(t, h, model, tt.body)

					rules := openai.DefaultResponseRules()
					harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func TestMultiTurnSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "My name is Alice."},
						},
					},
					{
						"type": "message",
						"role": "assistant",
						"content": []map[string]any{
							{"type": "output_text", "text": "Nice to meet you, Alice!"},
						},
					},
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "What is my name? Reply with just the name."},
						},
					},
				},
			}

			openaiEvents, wingmanEvents := compareSSE(t, h, model, body)

			rules := openai.DefaultSSEEventRules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
		})
	}
}
