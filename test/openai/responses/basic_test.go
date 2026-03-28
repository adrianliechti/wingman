package responses_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestBasicHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			tests := []struct {
				name string
				body map[string]any
			}{
				{
					name: "simple string input",
					body: map[string]any{
						"input": "Say hello and nothing else.",
					},
				},
				{
					name: "input items with user message",
					body: map[string]any{
						"input": []map[string]any{
							{
								"type": "message",
								"role": "user",
								"content": []map[string]any{
									{"type": "input_text", "text": "Say hello and nothing else."},
								},
							},
						},
					},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					openaiResp, wingmanResp := compareHTTP(t, h, model, tt.body)

					rules := openai.DefaultResponsesResponseRules()
					harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func TestBasicSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			tests := []struct {
				name string
				body map[string]any
			}{
				{
					name: "simple string input streaming",
					body: map[string]any{
						"input": "Say hello and nothing else.",
					},
				},
				{
					name: "input items streaming",
					body: map[string]any{
						"input": []map[string]any{
							{
								"type": "message",
								"role": "user",
								"content": []map[string]any{
									{"type": "input_text", "text": "Say hello and nothing else."},
								},
							},
						},
					},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					openaiEvents, wingmanEvents := compareSSE(t, h, model, tt.body)

					rules := openai.DefaultResponsesSSERules()
					harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
				})
			}
		})
	}
}
