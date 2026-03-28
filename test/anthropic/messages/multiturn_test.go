package messages_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestMultiTurnHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 100,
				"messages": []map[string]any{
					{"role": "user", "content": "My name is Alice."},
					{"role": "assistant", "content": "Nice to meet you, Alice!"},
					{"role": "user", "content": "What is my name? Reply with just the name."},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			rules := anthropic.DefaultMessagesResponseRules()
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestMultiTurnSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 100,
				"messages": []map[string]any{
					{"role": "user", "content": "My name is Alice."},
					{"role": "assistant", "content": "Nice to meet you, Alice!"},
					{"role": "user", "content": "What is my name? Reply with just the name."},
				},
			}

			anthropicEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

			rules := anthropic.DefaultMessagesSSERules()
			harness.CompareSSEStructureByType(t, anthropicEvents, wingmanEvents, rules)
		})
	}
}
