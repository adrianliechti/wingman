package responses_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	openaitest "github.com/adrianliechti/wingman/test/openai"
)

func TestMultiTurnHTTP(t *testing.T) {
	h := openaitest.New(t)
	ctx := context.Background()

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "user assistant user",
			body: map[string]any{
				"model": "gpt-5.4-mini",
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
				"model":        "gpt-5.4-mini",
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

			rules := openaitest.DefaultResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestMultiTurnSSE(t *testing.T) {
	h := openaitest.New(t)
	ctx := context.Background()

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "user assistant user streaming",
			body: map[string]any{
				"model":  "gpt-5.4-mini",
				"stream": true,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", tt.body)
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", tt.body)
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			if len(openaiEvents) == 0 {
				t.Fatal("openai returned no SSE events")
			}
			if len(wingmanEvents) == 0 {
				t.Fatal("wingman returned no SSE events")
			}

			harness.CompareSSEEventPattern(t, openaiEvents, wingmanEvents)

			rules := openaitest.DefaultSSEEventRules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
		})
	}
}
