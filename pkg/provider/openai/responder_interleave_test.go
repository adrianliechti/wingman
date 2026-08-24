package openai

import (
	"slices"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// TestResponderInput_InterleavedReasoningKeepsPairing verifies an assistant
// message with interleaved reasoning and tool calls replays in encounter
// order. The Responses API requires each reasoning item to be immediately
// followed by its paired item, or it rejects the request with "Item 'rs_...'
// of type 'reasoning' was provided without its required following item".
func TestResponderInput_InterleavedReasoningKeepsPairing(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	messages := []provider.Message{
		provider.UserMessage("run two steps"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Signature: "sig1"}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "step_one", Arguments: "{}"}),
				provider.ReasoningContent(provider.Reasoning{ID: "rs_2", Signature: "sig2"}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_2", Name: "step_two", Arguments: "{}"}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "ok"}}}),
				provider.ToolResultContent(provider.ToolResult{ID: "call_2", Parts: []provider.Part{{Text: "ok"}}}),
			},
		},
	}

	body := responsesRequestBody(t, responder, messages, &provider.CompleteOptions{})

	var sequence []string
	for _, item := range body["input"].([]any) {
		m := item.(map[string]any)
		switch m["type"] {
		case "reasoning":
			sequence = append(sequence, m["id"].(string))
		case "function_call":
			sequence = append(sequence, m["call_id"].(string))
		}
	}

	want := []string{"rs_1", "call_1", "rs_2", "call_2"}
	if !slices.Equal(sequence, want) {
		t.Fatalf("sequence: got %v, want %v", sequence, want)
	}
}

// TestResponderInput_TextBetweenCallsKeepsOrder verifies assistant text
// emitted between tool calls stays between them instead of migrating before
// the calls.
func TestResponderInput_TextBetweenCallsKeepsOrder(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	messages := []provider.Message{
		provider.UserMessage("go"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "step_one", Arguments: "{}"}),
				provider.TextContent("halfway update"),
				provider.ToolCallContent(provider.ToolCall{ID: "call_2", Name: "step_two", Arguments: "{}"}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "ok"}}}),
				provider.ToolResultContent(provider.ToolResult{ID: "call_2", Parts: []provider.Part{{Text: "ok"}}}),
			},
		},
	}

	body := responsesRequestBody(t, responder, messages, &provider.CompleteOptions{})

	var sequence []string
	for _, item := range body["input"].([]any) {
		m := item.(map[string]any)
		switch m["type"] {
		case "function_call":
			sequence = append(sequence, m["call_id"].(string))
		case "message":
			if m["role"] == "assistant" {
				sequence = append(sequence, "text")
			}
		}
	}

	want := []string{"call_1", "text", "call_2"}
	if !slices.Equal(sequence, want) {
		t.Fatalf("sequence: got %v, want %v", sequence, want)
	}
}
