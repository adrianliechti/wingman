package features_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

func buildCompactionInput() []map[string]any {
	// Unique prefix per run: cached input tokens don't count towards the
	// compaction trigger, so a warm prompt cache would suppress compaction.
	seed := fmt.Sprintf("run-%d ", time.Now().UnixNano())
	padding := strings.Repeat("This is filler text to increase the token count of this conversation so that compaction triggers. ", 3000)

	return []map[string]any{
		{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": seed + "Remember: the secret code is ALPHA-7. " + padding},
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
	h := openai.New(t)

	h.Client.Timeout = 180 * time.Second

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Compaction {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": buildCompactionInput(),
				"context_management": []map[string]any{
					{
						"type":              "compaction",
						"compact_threshold": 50000,
					},
				},
			}

			// Reference parity only holds for the native model family; other
			// backends produce different output item sets.
			if !strings.HasPrefix(model.Name, "gpt") {
				h.RequireConfigured(t, model.Name)

				wingmanResp, err := h.Client.Post(context.Background(), h.Wingman, "/responses", responses.WithModel(body, model.Name))
				if err != nil {
					t.Fatalf("wingman request failed: %v", err)
				}
				if wingmanResp.StatusCode != 200 {
					t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
				}

				requireCompactionOutput(t, "wingman", wingmanResp.Body)
				return
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			requireCompactionOutput(t, "openai", openaiResp.Body)
			requireCompactionOutput(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestCompactionSSE(t *testing.T) {
	h := openai.New(t)

	h.Client.Timeout = 180 * time.Second

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Compaction {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": buildCompactionInput(),
				"context_management": []map[string]any{
					{
						"type":              "compaction",
						"compact_threshold": 50000,
					},
				},
			}

			// Reference parity only holds for the native model family; other
			// backends produce different output item sets.
			if !strings.HasPrefix(model.Name, "gpt") {
				h.RequireConfigured(t, model.Name)

				streamBody := responses.WithModel(body, model.Name)
				streamBody["stream"] = true

				wingmanEvents, err := h.Client.PostSSE(context.Background(), h.Wingman, "/responses", streamBody)
				if err != nil {
					t.Fatalf("wingman SSE request failed: %v", err)
				}

				requireCompactionSSEEvent(t, "wingman", wingmanEvents)
				return
			}

			openaiEvents, wingmanEvents := responses.CompareSSE(t, h, model, body)

			requireCompactionSSEEvent(t, "openai", openaiEvents)
			requireCompactionSSEEvent(t, "wingman", wingmanEvents)

			rules := openai.DefaultResponsesSSERules()
			harness.CompareSSEStructureByType(t, openaiEvents, wingmanEvents, rules)
		})
	}
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
			content, _ := obj["content"].(string)
			if enc == "" && content == "" {
				t.Errorf("[%s] compaction item has neither encrypted_content nor content", label)
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
