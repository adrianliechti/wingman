package chat_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestChatCompletionHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			tests := []struct {
				name string
				body map[string]any
			}{
				{
					name: "simple message",
					body: map[string]any{
						"messages": []map[string]any{
							{"role": "user", "content": "Say hello and nothing else."},
						},
					},
				},
				{
					name: "with system message",
					body: map[string]any{
						"messages": []map[string]any{
							{"role": "system", "content": "You are a helpful assistant. Always respond in exactly one word."},
							{"role": "user", "content": "What is the capital of France?"},
						},
					},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					openaiResp, wingmanResp := compareHTTP(t, h, model, tt.body)

					rules := openai.DefaultChatResponseRules()
					harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func TestChatCompletionSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "Say hello and nothing else."},
				},
			}

			openaiEvents, wingmanEvents := compareSSE(t, h, model, body)

			rules := openai.DefaultChatSSEEventRules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
		})
	}
}
