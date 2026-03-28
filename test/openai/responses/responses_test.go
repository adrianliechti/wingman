package responses_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
)

func TestResponsesHTTP(t *testing.T) {
	h := harness.New(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		model string
		body  map[string]any
	}{
		{
			name:  "simple string input",
			model: "gpt-5.4-mini",
			body: map[string]any{
				"model": "gpt-5.4-mini",
				"input": "Say hello and nothing else.",
			},
		},
		{
			name:  "input items with user message",
			model: "gpt-5.4-mini",
			body: map[string]any{
				"model": "gpt-5.4-mini",
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
			openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/responses", tt.body)
			if err != nil {
				t.Fatalf("openai request failed: %v", err)
			}

			wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/responses", tt.body)
			if err != nil {
				t.Fatalf("wingman request failed: %v", err)
			}

			// Both should return 200
			if openaiResp.StatusCode != 200 {
				t.Fatalf("openai returned status %d: %s", openaiResp.StatusCode, string(openaiResp.RawBody))
			}
			if wingmanResp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
			}

			rules := harness.DefaultResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestResponsesSSE(t *testing.T) {
	h := harness.New(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		model string
		body  map[string]any
	}{
		{
			name:  "simple string input streaming",
			model: "gpt-5.4-mini",
			body: map[string]any{
				"model":  "gpt-5.4-mini",
				"stream": true,
				"input":  "Say hello and nothing else.",
			},
		},
		{
			name:  "input items streaming",
			model: "gpt-5.4-mini",
			body: map[string]any{
				"model":  "gpt-5.4-mini",
				"stream": true,
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

			// Compare event type sequence
			harness.CompareSSEEventTypes(t, openaiEvents, wingmanEvents)

			// Compare structural shape of each event
			rules := harness.DefaultSSEEventRules()
			harness.CompareSSEStructure(t, openaiEvents, wingmanEvents, rules)
		})
	}
}
