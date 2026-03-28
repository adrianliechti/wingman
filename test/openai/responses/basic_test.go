package responses_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	openaitest "github.com/adrianliechti/wingman/test/openai"
)

func TestResponsesHTTP(t *testing.T) {
	h := openaitest.New(t)
	ctx := context.Background()

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "simple string input",
			body: map[string]any{
				"model": "gpt-5.4-mini",
				"input": "Say hello and nothing else.",
			},
		},
		{
			name: "input items with user message",
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

func TestResponsesSSE(t *testing.T) {
	h := openaitest.New(t)
	ctx := context.Background()

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "simple string input streaming",
			body: map[string]any{
				"model":  "gpt-5.4-mini",
				"stream": true,
				"input":  "Say hello and nothing else.",
			},
		},
		{
			name: "input items streaming",
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

			harness.CompareSSEEventPattern(t, openaiEvents, wingmanEvents)

			rules := openaitest.DefaultSSEEventRules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
		})
	}
}
