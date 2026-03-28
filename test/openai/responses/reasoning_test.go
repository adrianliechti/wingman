package responses_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	openaitest "github.com/adrianliechti/wingman/test/openai"
)

var reasoningTests = []struct {
	name             string
	body             map[string]any
	requireReasoning bool // whether reasoning output must be present
}{
	{
		name:             "reasoning with summary",
		requireReasoning: true,
		body: map[string]any{
			"model": "gpt-5.4-mini",
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
			"model": "gpt-5.4-mini",
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
	h := openaitest.New(t)
	ctx := context.Background()

	for _, tt := range reasoningTests {
		t.Run(tt.name, func(t *testing.T) {
			openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/responses", tt.body)
			if err != nil {
				t.Fatalf("openai request failed: %v", err)
			}

			wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/responses", tt.body)
			if err != nil {
				t.Fatalf("wingman request failed: %v", err)
			}

			if openaiResp.StatusCode != 200 {
				t.Fatalf("openai returned status %d: %s", openaiResp.StatusCode, string(openaiResp.RawBody))
			}
			if wingmanResp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
			}

			if tt.requireReasoning {
				requireReasoningOutput(t, "openai", openaiResp.Body)
				requireReasoningOutput(t, "wingman", wingmanResp.Body)
			}

			rules := openaitest.DefaultResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestReasoningSSE(t *testing.T) {
	h := openaitest.New(t)
	ctx := context.Background()

	for _, tt := range reasoningTests {
		streamBody := make(map[string]any)
		for k, v := range tt.body {
			streamBody[k] = v
		}
		streamBody["stream"] = true

		t.Run(tt.name, func(t *testing.T) {
			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", streamBody)
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", streamBody)
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			if len(openaiEvents) == 0 {
				t.Fatal("openai returned no SSE events")
			}
			if len(wingmanEvents) == 0 {
				t.Fatal("wingman returned no SSE events")
			}

			if tt.requireReasoning {
				requireReasoningSSEEvent(t, "openai", openaiEvents)
				requireReasoningSSEEvent(t, "wingman", wingmanEvents)
			}

			harness.CompareSSEEventPattern(t, openaiEvents, wingmanEvents)

			rules := openaitest.DefaultSSEEventRules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
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
			summary, _ := obj["summary"].([]any)
			if len(summary) == 0 {
				t.Errorf("[%s] reasoning item has no summary", label)
			}
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
