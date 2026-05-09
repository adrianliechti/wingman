package claude

import (
	"encoding/json"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// convertCliContent maps a CLI assistant content block to the provider type.
// Returns ok=false for blocks that have no caller-visible payload.
func convertCliContent(block cliContent) (provider.Content, bool) {
	switch block.Type {
	case "text":
		if block.Text == "" {
			return provider.Content{}, false
		}
		return provider.TextContent(block.Text), true

	case "thinking":
		if block.Thinking == "" && block.Signature == "" {
			return provider.Content{}, false
		}
		return provider.ReasoningContent(provider.Reasoning{
			Text:      block.Thinking,
			Signature: block.Signature,
		}), true

	case "tool_use":
		args := string(block.Input)
		if args == "" {
			args = "{}"
		}
		return provider.ToolCallContent(provider.ToolCall{
			ID:        block.ID,
			Name:      stripToolPrefix(block.Name),
			Arguments: args,
		}), true

	case "tool_result":
		var data string
		if len(block.ResultData) > 0 {
			// tool_result.content can be a string or an array of blocks.
			var s string
			if err := json.Unmarshal(block.ResultData, &s); err == nil {
				data = s
			} else {
				data = string(block.ResultData)
			}
		}
		return provider.ToolResultContent(provider.ToolResult{
			ID:   block.ToolUseID,
			Data: data,
		}), true

	case "refusal":
		if block.Refusal == "" {
			return provider.Content{}, false
		}
		return provider.RefusalContent(block.Refusal), true
	}

	return provider.Content{}, false
}

// yieldContent yields a single content block as a Completion delta. Returns
// false if the consumer cancelled the iterator.
func yieldContent(yield func(*provider.Completion, error) bool, id, model string, content provider.Content) bool {
	delta := &provider.Completion{
		ID:    id,
		Model: model,
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{content},
		},
	}
	return yield(delta, nil)
}
