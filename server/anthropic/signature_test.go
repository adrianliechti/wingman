package anthropic

import (
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func TestEmptyThinkingReplaysWithoutSyntheticOpenAISummary(t *testing.T) {
	requests := 0
	c, err := openai.NewResponder("https://api.openai.com", "gpt-test", openai.WithToken("test"), openai.WithClient(featureClient(t, responsesFeatureStream, func(_ *http.Request, sent map[string]any) {
		requests++
		found := false
		for _, raw := range sent["input"].([]any) {
			item := raw.(map[string]any)
			if item["type"] != "reasoning" {
				continue
			}
			found = true
			summary, ok := item["summary"].([]any)
			if !ok || len(summary) != 0 || item["encrypted_content"] != "opaque" || item["id"] != "rs_original" {
				t.Errorf("signature-only thinking gained a placeholder: %v", item)
			}
		}
		if !found {
			t.Error("reasoning disappeared from replay")
		}
	})))
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []bool{false, true} {
		rec := featurePost(t, featureRouter(c), "/messages", map[string]any{
			"model": "target", "max_tokens": 1000, "stream": stream,
			"messages": []any{
				map[string]any{"role": "user", "content": "hello"},
				map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "thinking", "thinking": "", "signature": encodeSignature("rs_original", "@target:opaque")},
					map[string]any{"type": "text", "text": "hello"},
				}},
				map[string]any{"role": "user", "content": "continue"},
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}
	}
	if requests != 2 {
		t.Fatalf("upstream requests = %d", requests)
	}
}

func TestSignatureCodec(t *testing.T) {
	if got := encodeSignature("", "SIG"); got != "SIG" {
		t.Fatalf("expected bare signature without id, got %q", got)
	}

	if got := encodeSignature("rs_1", ""); got != "" {
		t.Fatalf("expected empty for missing signature, got %q", got)
	}

	id, signature := decodeSignature(encodeSignature("rs_1", "BLOB"))
	if id != "rs_1" || signature != "BLOB" {
		t.Fatalf("round-trip mismatch: %q %q", id, signature)
	}

	id, signature = decodeSignature("RAW")
	if id != "" || signature != "RAW" {
		t.Fatalf("expected raw value as signature, got %q %q", id, signature)
	}

	tagged := encodeSignature("rs_1", "@gpt-5:BLOB")
	if realm, raw := provider.ParseSignature(tagged); realm != "gpt-5" || len(raw) == 0 {
		t.Fatalf("expected outer realm tag on wrapped signature, got %q (realm=%q raw=%q)", tagged, realm, raw)
	}
	if id, signature = decodeSignature(tagged); id != "rs_1" || signature != "@gpt-5:BLOB" {
		t.Fatalf("tagged round-trip mismatch: %q %q", id, signature)
	}
}

func TestStopReasonIgnoresServerToolSearchAndNilMessage(t *testing.T) {
	completion := &provider.Completion{
		Status: provider.CompletionStatusCompleted,
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "srv_1",
					Name:      "tool_search_tool_regex",
					Kind:      provider.ToolKindToolSearch,
					Execution: "server",
					Arguments: `{"pattern":"lookup"}`,
				}),
				provider.TextContent("Found it."),
			},
		},
	}
	if got := toStopReason(completion); got != StopReasonEndTurn {
		t.Fatalf("expected end_turn after hosted tool search, got %q", got)
	}

	var stop StopReason
	acc := NewStreamingAccumulator("msg_1", "claude-test", func(event StreamEvent) error {
		if event.Type == StreamEventMessageDelta && event.MessageDelta != nil {
			stop = event.MessageDelta.StopReason
		}
		return nil
	})
	if err := acc.Add(provider.Completion{StopReason: provider.StopReasonMaxTokens}); err != nil {
		t.Fatal(err)
	}
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}
	if stop != StopReasonMaxTokens {
		t.Fatalf("expected max_tokens stop reason on message-less completion, got %q", stop)
	}
}

func TestThinkingBlockRoundTrip(t *testing.T) {
	blocks := toContentBlocks([]provider.Content{
		provider.ReasoningContent(provider.Reasoning{
			ID:        "rs_1",
			Summary:   provider.TextParts("the summary"),
			Signature: "BLOB",
		}),
	})

	if len(blocks) != 1 || blocks[0].Type != "thinking" {
		t.Fatalf("expected one thinking block, got %+v", blocks)
	}

	if blocks[0].Thinking != "the summary" {
		t.Fatalf("expected summary as thinking text, got %q", blocks[0].Thinking)
	}

	messages, err := toMessages("", []MessageParam{{
		Role: MessageRoleAssistant,
		Content: []ContentBlockParam{{
			Type:      "thinking",
			Thinking:  blocks[0].Thinking,
			Signature: blocks[0].Signature,
		}},
	}})

	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}

	var reasoning *provider.Reasoning

	for _, m := range messages {
		for _, c := range m.Content {
			if c.Reasoning != nil {
				reasoning = c.Reasoning
			}
		}
	}

	if reasoning == nil {
		t.Fatal("expected reasoning content")
	}

	if reasoning.ID != "rs_1" || reasoning.Signature != "BLOB" || reasoning.SummaryText() != "the summary" || reasoning.Text() != "" {
		t.Fatalf("unexpected reasoning: %+v", reasoning)
	}
}
