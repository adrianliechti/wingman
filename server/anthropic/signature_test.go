package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

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
}

func TestThinkingBlockRoundTrip(t *testing.T) {
	blocks := toContentBlocks([]provider.Content{
		provider.ReasoningContent(provider.Reasoning{
			ID:        "rs_1",
			Summary:   "the summary",
			Signature: "BLOB",
		}),
	}, true)

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

	if reasoning.ID != "rs_1" || reasoning.Signature != "BLOB" || reasoning.Summary != "the summary" || reasoning.Text != "" {
		t.Fatalf("unexpected reasoning: %+v", reasoning)
	}
}
