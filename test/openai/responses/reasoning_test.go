package responses_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

var reasoningTests = []struct {
	name             string
	body             map[string]any
	requireReasoning bool
}{
	{
		name:             "reasoning with summary",
		requireReasoning: true,
		body: map[string]any{
			"input": "How many r's are in strawberry?",
			"reasoning": map[string]any{
				"effort":  "low",
				"summary": "auto",
			},
		},
	},
	{
		name:             "reasoning multi-turn",
		requireReasoning: true,
		body: map[string]any{
			"reasoning": map[string]any{
				"effort":  "high",
				"summary": "auto",
			},
			"input": []map[string]any{
				{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{"type": "input_text", "text": "Count the number of letter 'e' in the word 'nevertheless'."},
					},
				},
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": "There are 3 letter e's in 'nevertheless'."},
					},
				},
				{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{"type": "input_text", "text": "Are you sure? Count again very carefully, letter by letter."},
					},
				},
			},
		},
	},
}

func TestReasoningHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Reasoning {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			for _, tt := range reasoningTests {
				t.Run(tt.name, func(t *testing.T) {
					openaiResp, wingmanResp := compareHTTP(t, h, model, tt.body)

					if tt.requireReasoning {
						requireReasoningOutput(t, "openai", openaiResp.Body)
						requireReasoningOutput(t, "wingman", wingmanResp.Body)
					}

					rules := openai.DefaultResponseRules()
					harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func TestReasoningSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Reasoning {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			for _, tt := range reasoningTests {
				t.Run(tt.name, func(t *testing.T) {
					openaiEvents, wingmanEvents := compareSSE(t, h, model, tt.body)

					if tt.requireReasoning {
						requireReasoningSSEEvent(t, "openai", openaiEvents)
						requireReasoningSSEEvent(t, "wingman", wingmanEvents)
					}

					rules := openai.DefaultSSEEventRules()
					harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
				})
			}
		})
	}
}

func requireReasoningOutput(t *testing.T, label string, body map[string]any) {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "reasoning" {
			return
		}
	}

	t.Fatalf("[%s] no reasoning output item found", label)
}

func requireReasoningSSEEvent(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		itemType, _ := e.Data["type"].(string)
		if itemType != "response.output_item.added" {
			continue
		}

		item, ok := e.Data["item"].(map[string]any)
		if !ok {
			continue
		}

		if item["type"] == "reasoning" {
			return
		}
	}

	t.Fatalf("[%s] no reasoning SSE event found", label)
}
