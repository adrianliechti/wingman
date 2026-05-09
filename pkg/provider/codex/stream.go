package codex

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

// yieldText emits a streaming text delta as a Completion.
func yieldText(yield func(*provider.Completion, error) bool, id, model, delta string) bool {
	if delta == "" {
		return true
	}
	return yield(&provider.Completion{
		ID:    id,
		Model: model,
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{provider.TextContent(delta)},
		},
	}, nil)
}

// yieldReasoning emits a streaming reasoning delta as a Completion.
func yieldReasoning(yield func(*provider.Completion, error) bool, id, model, delta string) bool {
	if delta == "" {
		return true
	}
	return yield(&provider.Completion{
		ID:    id,
		Model: model,
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Text: delta}),
			},
		},
	}, nil)
}

// yieldToolCall emits a tool-call content block as a Completion.
func yieldToolCall(yield func(*provider.Completion, error) bool, id, model string, call provider.ToolCall) bool {
	return yield(&provider.Completion{
		ID:    id,
		Model: model,
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(call)},
		},
	}, nil)
}
