package messages_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestBasicHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			tests := []struct {
				name string
				body map[string]any
			}{
				{
					name: "simple message",
					body: map[string]any{
						"max_tokens": 100,
						"messages": []map[string]any{
							{"role": "user", "content": "Say hello and nothing else."},
						},
					},
				},
				{
					name: "with system prompt",
					body: map[string]any{
						"max_tokens": 100,
						"system":     "You are a helpful assistant. Always respond in exactly one word.",
						"messages": []map[string]any{
							{"role": "user", "content": "What is the capital of France?"},
						},
					},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, tt.body)

					rules := anthropic.DefaultMessagesResponseRules()
					harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func TestBasicSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 100,
				"messages": []map[string]any{
					{"role": "user", "content": "Say hello and nothing else."},
				},
			}

			anthropicEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

			rules := anthropic.DefaultMessagesSSERules()
			harness.CompareSSEStructureByType(t, anthropicEvents, wingmanEvents, rules)
		})
	}
}
