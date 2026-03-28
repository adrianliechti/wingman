package messages_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestThinkingHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 16000,
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": 5000,
				},
				"messages": []map[string]any{
					{"role": "user", "content": "How many r's are in strawberry?"},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireThinkingBlock(t, "anthropic", anthropicResp.Body)
			requireThinkingBlock(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content.*.thinking"] = harness.FieldIgnore
			rules["content.*.signature"] = harness.FieldNonEmpty
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestThinkingSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 16000,
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": 5000,
				},
				"messages": []map[string]any{
					{"role": "user", "content": "How many r's are in strawberry?"},
				},
			}

			anthropicEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

			requireThinkingSSEEvent(t, "anthropic", anthropicEvents)
			requireThinkingSSEEvent(t, "wingman", wingmanEvents)
		})
	}
}

func TestThinkingMultiTurnHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 16000,
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": 5000,
				},
				"messages": []map[string]any{
					{"role": "user", "content": "Count the letter 'e' in 'nevertheless'."},
					{"role": "assistant", "content": "There are 3 letter e's in 'nevertheless'."},
					{"role": "user", "content": "Are you sure? Count again carefully, letter by letter."},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireThinkingBlock(t, "anthropic", anthropicResp.Body)
			requireThinkingBlock(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content.*.thinking"] = harness.FieldIgnore
			rules["content.*.signature"] = harness.FieldNonEmpty
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestStopReasonEndTurnHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 100,
				"messages": []map[string]any{
					{"role": "user", "content": "Say hello and nothing else."},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireStopReason(t, "anthropic", anthropicResp.Body, "end_turn")
			requireStopReason(t, "wingman", wingmanResp.Body, "end_turn")
		})
	}
}

func TestStopReasonMaxTokensHTTP(t *testing.T) {
	h := anthropic.New(t)

	body := map[string]any{
		"max_tokens": 5,
		"messages": []map[string]any{
			{"role": "user", "content": "Write a 5000 word essay about the complete history of computing."},
		},
	}

	// Use haiku to avoid adaptive thinking budget interference
	anthropicResp, wingmanResp := compareHTTP(t, h, "claude-haiku-4-5", body)

	requireStopReason(t, "anthropic", anthropicResp.Body, "max_tokens")
	requireStopReason(t, "wingman", wingmanResp.Body, "max_tokens")
}

func TestStopReasonToolUseHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{weatherTool},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			requireStopReason(t, "anthropic", anthropicResp.Body, "tool_use")
			requireStopReason(t, "wingman", wingmanResp.Body, "tool_use")
		})
	}
}

func requireThinkingBlock(t *testing.T, label string, body map[string]any) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	for _, block := range content {
		obj, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "thinking" {
			thinking, _ := obj["thinking"].(string)
			if thinking == "" {
				t.Errorf("[%s] thinking block has empty thinking text", label)
			}
			signature, _ := obj["signature"].(string)
			if signature == "" {
				t.Errorf("[%s] thinking block has empty signature", label)
			}
			return
		}
	}

	t.Fatalf("[%s] no thinking content block found", label)
}

func requireThinkingSSEEvent(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		eventType, _ := e.Data["type"].(string)
		if eventType != "content_block_start" {
			continue
		}

		block, ok := e.Data["content_block"].(map[string]any)
		if !ok {
			continue
		}

		if block["type"] == "thinking" {
			return
		}
	}

	t.Fatalf("[%s] no thinking content_block_start SSE event found", label)
}

func requireStopReason(t *testing.T, label string, body map[string]any, expected string) {
	t.Helper()

	reason, _ := body["stop_reason"].(string)
	if reason != expected {
		t.Errorf("[%s] stop_reason = %q, want %q", label, reason, expected)
	}
}
