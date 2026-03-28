package chat_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "My name is Alice."},
					{"role": "assistant", "content": "Nice to meet you, Alice!"},
					{"role": "user", "content": "What is my name? Reply with just the name."},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			rules := openai.DefaultChatResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestMultiTurnSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": "My name is Alice."},
					{"role": "assistant", "content": "Nice to meet you, Alice!"},
					{"role": "user", "content": "What is my name? Reply with just the name."},
				},
			}

			openaiEvents, wingmanEvents := compareSSE(t, h, model, body)

			rules := openai.DefaultChatSSERules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
		})
	}
}
