package provider

// LastAssistantToolCallIsUnsigned reports whether the latest assistant turn
// contains a tool call but no signed reasoning block.
func LastAssistantToolCallIsUnsigned(messages []Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != MessageRoleAssistant {
			continue
		}

		var hasToolCall bool
		for _, content := range message.Content {
			if content.Reasoning != nil && content.Reasoning.Signature != "" {
				return false
			}
			hasToolCall = hasToolCall || content.ToolCall != nil
		}

		return hasToolCall
	}

	return false
}
