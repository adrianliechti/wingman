package provider

type InstructionScope string

const (
	InstructionScopeConversation InstructionScope = "conversation"
	InstructionScopeTurn         InstructionScope = "turn"
)

// Instructions supplies guidance in a system message at this conversation position.
// An empty scope means conversation. Turn scope expires at the next user
// message, including client tool results, but not hosted tool-search results.
type Instructions struct {
	Text  string
	Scope InstructionScope
}

// ResolveInstructions lowers scoped instructions for providers without native
// lifetimes. Expired instructions are omitted; active ones remain system text.
// The source history is unchanged and can still be replayed to a native provider.
func ResolveInstructions(messages []Message) []Message {
	found := false
	for _, message := range messages {
		for _, part := range message.Content {
			found = found || part.Instructions != nil
		}
	}
	if !found {
		return messages
	}
	result := make([]Message, len(messages))
	laterUser := false
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		var content []Content
		for _, part := range message.Content {
			if instruction := part.Instructions; instruction != nil {
				part.Instructions = nil
				if instruction.Scope != InstructionScopeTurn || !laterUser {
					content = append(content, TextContent(instruction.Text))
				}
				// Parts are normally disjoint, but retain any accompanying data.
				if part == (Content{}) {
					continue
				}
			}
			content = append(content, part)
		}
		if message.Role == MessageRoleUser {
			for _, part := range message.Content {
				if part.ToolResult == nil || part.ToolResult.Kind != ToolKindToolSearch || part.ToolResult.Execution == "client" {
					laterUser = true
				}
			}
		}
		message.Content = content
		result[i] = message
	}
	filtered := result[:0]
	for _, message := range result {
		if len(message.Content) > 0 {
			filtered = append(filtered, message)
		}
	}
	return filtered
}
