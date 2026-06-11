package router

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestScrubMessages(t *testing.T) {
	messages := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Text: "thinking", Signature: "SIG"}),
				provider.CompactionContent(provider.Compaction{Signature: "SIG"}),
				provider.TextContent("answer"),
			},
		},
	}

	result := ScrubMessages(messages)

	if len(result) != 1 || len(result[0].Content) != 1 {
		t.Fatalf("expected 1 message with 1 content block, got %+v", result)
	}

	if result[0].Content[0].Text != "answer" {
		t.Errorf("expected text content to survive, got %+v", result[0].Content[0])
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

	if result.ReasoningOptions.IncludeSignature {
		t.Error("expected IncludeSignature disabled")
	}

	if result.CompactionOptions != nil {
		t.Error("expected CompactionOptions removed")
	}

	if !options.ReasoningOptions.IncludeSignature || options.CompactionOptions == nil {
		t.Error("original options mutated")
	}
}

// A compaction-continuation history starts with an assistant message holding
// only the compaction block; scrubbing must drop the whole message instead of
// sending an empty one upstream.
func TestScrubMessages_DropsEmptiedMessages(t *testing.T) {
	result := ScrubMessages([]provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.CompactionContent(provider.Compaction{Signature: "ENC"}),
			},
		},
		provider.UserMessage("continue"),
	})

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(result), result)
	}

	if result[0].Role != provider.MessageRoleUser {
		t.Errorf("expected user message to survive, got %+v", result[0])
	}
}

func TestScrubMessages_DropsRedactedReasoning(t *testing.T) {
	result := ScrubMessages([]provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Signature: "BLOB", Redacted: true}),
				provider.TextContent("answer"),
			},
		},
	})

	if len(result) != 1 || len(result[0].Content) != 1 || result[0].Content[0].Text != "answer" {
		t.Fatalf("expected only text content to survive, got %+v", result)
	}
}
