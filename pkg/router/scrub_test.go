package router

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestScrubMessages(t *testing.T) {
	messages := []provider.Message{
		{
			Role:  provider.MessageRoleAssistant,
			Phase: provider.MessagePhaseCommentary,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Text: "thinking", Signature: "SIG"}),
				provider.CompactionContent(provider.Compaction{Signature: "SIG"}),
				provider.TextContent("answer"),
			},
		},
	}

	result := ScrubMessages(messages)

	if len(result) != 1 || len(result[0].Content) != 3 {
		t.Fatalf("expected reasoning, compaction and text to survive, got %+v", result)
	}

	// signatures stay: the scoped member decides whether they replay
	if result[0].Content[0].Reasoning == nil || result[0].Content[0].Reasoning.Signature != "SIG" {
		t.Errorf("expected reasoning to survive, got %+v", result[0].Content[0])
	}

	if result[0].Content[1].Compaction == nil || result[0].Content[1].Compaction.Signature != "SIG" {
		t.Errorf("expected compaction to survive, got %+v", result[0].Content[1])
	}

	if result[0].Phase != provider.MessagePhaseCommentary {
		t.Errorf("expected message phase to survive, got %q", result[0].Phase)
	}
}

func TestScrubMessages_ToolIDSignatures(t *testing.T) {
	const signedID = "call_1::search::U0VDUkVUX1NJRw=="

	messages := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        signedID,
					Name:      "search",
					Arguments: `{}`,
				}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{
					ID: signedID,
				}),
			},
		},
	}

	result := ScrubMessages(messages)

	call := result[0].Content[0].ToolCall
	if call == nil || call.ID != "call_1::search" {
		t.Errorf("expected tool call ID stripped to \"call_1::search\", got %+v", call)
	}

	res := result[1].Content[0].ToolResult
	if res == nil || res.ID != "call_1::search" {
		t.Errorf("expected tool result ID stripped to \"call_1::search\", got %+v", res)
	}

	// Plain IDs pass through untouched.
	plain := ScrubMessages([]provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{ID: "call_2", Name: "search"}),
			},
		},
	})

	if got := plain[0].Content[0].ToolCall.ID; got != "call_2" {
		t.Errorf("expected plain tool call ID unchanged, got %q", got)
	}

	// The originals must not be mutated.
	if messages[0].Content[0].ToolCall.ID != signedID {
		t.Errorf("original tool call mutated: %q", messages[0].Content[0].ToolCall.ID)
	}

	if messages[1].Content[0].ToolResult.ID != signedID {
		t.Errorf("original tool result mutated: %q", messages[1].Content[0].ToolResult.ID)
	}
}

func TestScrubOptions(t *testing.T) {
	if got := ScrubOptions(nil); got != nil {
		t.Errorf("expected nil options to pass through, got %+v", got)
	}

	options := &provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{
			IncludeSignature: true,
		},

		CompactionOptions: &provider.CompactionOptions{Threshold: 1000},
	}

	result := ScrubOptions(options)

	if !result.ReasoningOptions.IncludeSignature {
		t.Error("expected IncludeSignature kept")
	}

	if result.CompactionOptions != nil {
		t.Error("expected CompactionOptions removed")
	}

	if !options.ReasoningOptions.IncludeSignature || options.CompactionOptions == nil {
		t.Error("original options mutated")
	}
}

func TestScrubMessages_KeepsRedactedReasoning(t *testing.T) {
	result := ScrubMessages([]provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Signature: "BLOB", Redacted: true}),
				provider.TextContent("answer"),
			},
		},
	})

	if len(result) != 1 || len(result[0].Content) != 2 || !result[0].Content[0].Reasoning.Redacted {
		t.Fatalf("expected redacted reasoning to reach the scoped member, got %+v", result)
	}
}
