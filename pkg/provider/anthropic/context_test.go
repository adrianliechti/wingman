package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestThinkingRetentionRequest(t *testing.T) {
	c, _ := NewCompleter("http://localhost", "claude-sonnet-4-6")
	for _, mode := range []provider.ReasoningContext{provider.ReasoningContextAuto, provider.ReasoningContextAllTurns, provider.ReasoningContextCurrentTurn} {
		t.Run(string(mode), func(t *testing.T) {
			options := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Context: mode}}
			body := requestBody(t, c, []provider.Message{provider.UserMessage("Hi")}, options)
			if mode == provider.ReasoningContextAuto {
				if body["context_management"] != nil {
					t.Fatalf("auto should preserve the model's default: %v", body)
				}
				return
			}
			edits := body["context_management"].(map[string]any)["edits"].([]any)
			edit := edits[0].(map[string]any)
			if len(edits) != 1 || edit["type"] != "clear_thinking_20251015" {
				t.Fatalf("missing thinking retention: %v", edits)
			}
			if mode == provider.ReasoningContextAllTurns && edit["keep"] != "all" {
				t.Fatalf("lost all-turn retention: %v", edit)
			}
			if mode == provider.ReasoningContextCurrentTurn {
				keep := edit["keep"].(map[string]any)
				if keep["type"] != "thinking_turns" || keep["value"] != float64(1) {
					t.Fatalf("lost current-turn retention: %v", keep)
				}
			}
			options.CompactionOptions = &provider.CompactionOptions{Threshold: 50000}
			body = requestBody(t, c, []provider.Message{provider.UserMessage("Hi")}, options)
			edits = body["context_management"].(map[string]any)["edits"].([]any)
			if len(edits) != 2 || edits[0].(map[string]any)["type"] != "clear_thinking_20251015" || edits[1].(map[string]any)["type"] != "compact_20260112" {
				t.Fatalf("compaction replaced or reordered thinking retention: %v", edits)
			}
		})
	}
}

func TestThinkingRetentionOmittedWhenThinkingDisabled(t *testing.T) {
	c, _ := NewCompleter("http://localhost", "claude-sonnet-4-6")
	unsigned := []provider.Message{
		provider.UserMessage("Read the file"),
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "t", Name: "Read", Arguments: "{}"})}},
		provider.ToolMessage("t", "contents"),
	}
	for _, tc := range []struct {
		name    string
		history []provider.Message
		opts    provider.CompleteOptions
	}{
		{"disabled", nil, provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled, Context: provider.ReasoningContextAllTurns}}},
		{"default", nil, provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Context: provider.ReasoningContextAllTurns}}},
		{"unsigned_tool_turn", unsigned, provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Context: provider.ReasoningContextAllTurns}}},
		{"forced_tool", nil, provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Context: provider.ReasoningContextAllTurns}, ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceAny}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := requestBody(t, c, tc.history, &tc.opts)
			if body["context_management"] != nil {
				t.Fatalf("inactive thinking must not send a retention edit: %v", body)
			}
		})
	}
}
