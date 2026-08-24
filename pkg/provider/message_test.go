package provider

import "testing"

func TestLastAssistantToolCallIsUnsignedUsesLatestTurn(t *testing.T) {
	messages := []Message{
		UserMessage("first"),
		{
			Role: MessageRoleAssistant,
			Content: []Content{
				ReasoningContent(Reasoning{Signature: "old-signature"}),
				ToolCallContent(ToolCall{ID: "call_1"}),
			},
		},
		UserMessage("next"),
		{
			Role: MessageRoleAssistant,
			Content: []Content{
				ToolCallContent(ToolCall{ID: "call_2"}),
			},
		},
		UserMessage("result"),
	}

	if !LastAssistantToolCallIsUnsigned(messages) {
		t.Fatal("older signature must not satisfy the latest assistant tool turn")
	}

	messages[3].Content = append([]Content{
		ReasoningContent(Reasoning{Signature: "latest-signature"}),
	}, messages[3].Content...)
	if LastAssistantToolCallIsUnsigned(messages) {
		t.Fatal("latest assistant signature should satisfy the tool turn")
	}
}
