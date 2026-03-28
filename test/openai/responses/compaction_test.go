package responses_test

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	openaitest "github.com/adrianliechti/wingman/test/openai"
)

func buildCompactionInput() []map[string]any {
	padding := strings.Repeat("This is filler text to increase the token count of this conversation so that compaction triggers. ", 80)

	return []map[string]any{
		{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "Remember: the secret code is ALPHA-7. " + padding},
			},
		},
		{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": "I'll remember the secret code ALPHA-7. " + padding},
			},
		},
		{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "What is 2+2? Reply with just the number."},
			},
		},
	}
}

func TestCompactionHTTP(t *testing.T) {
	h := openaitest.New(t)
	ctx := context.Background()

	body := map[string]any{
		"model": "gpt-5.4-mini",
		"input": buildCompactionInput(),
		"context_management": []map[string]any{
			{
				"type":              "compaction",
				"compact_threshold": 1000,
			},
		},
	}

	openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/responses", body)
	if err != nil {
		t.Fatalf("openai request failed: %v", err)
	}

	wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/responses", body)
	if err != nil {
		t.Fatalf("wingman request failed: %v", err)
	}

	if openaiResp.StatusCode != 200 {
		t.Fatalf("openai returned status %d: %s", openaiResp.StatusCode, string(openaiResp.RawBody))
	}
	if wingmanResp.StatusCode != 200 {
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	requireCompactionOutput(t, "openai", openaiResp.Body)
	requireCompactionOutput(t, "wingman", wingmanResp.Body)

	rules := openaitest.DefaultResponseRules()
	harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func TestCompactionSSE(t *testing.T) {
	h := openaitest.New(t)
	ctx := context.Background()

	body := map[string]any{
		"model":  "gpt-5.4-mini",
		"stream": true,
		"input":  buildCompactionInput(),
		"context_management": []map[string]any{
			{
				"type":              "compaction",
				"compact_threshold": 1000,
			},
		},
	}

	openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", body)
	if err != nil {
		t.Fatalf("openai SSE request failed: %v", err)
	}

	wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", body)
	if err != nil {
		t.Fatalf("wingman SSE request failed: %v", err)
	}

	if len(openaiEvents) == 0 {
		t.Fatal("openai returned no SSE events")
	}
	if len(wingmanEvents) == 0 {
		t.Fatal("wingman returned no SSE events")
	}

	requireCompactionSSEEvent(t, "openai", openaiEvents)
	requireCompactionSSEEvent(t, "wingman", wingmanEvents)

	harness.CompareSSEEventPattern(t, openaiEvents, wingmanEvents)

	rules := openaitest.DefaultSSEEventRules()
	harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
}

func requireCompactionOutput(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] == "compaction" {
			enc, _ := obj["encrypted_content"].(string)
			if enc == "" {
				t.Errorf("[%s] compaction item has empty encrypted_content", label)
			}
			return
		}
	}

	t.Fatalf("[%s] no compaction output item found", label)
}

func requireCompactionSSEEvent(t *testing.T, label string, events []*harness.SSEEvent) {
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

		if item["type"] == "compaction" {
			return
		}
	}

	t.Fatalf("[%s] no compaction SSE event found", label)
}
