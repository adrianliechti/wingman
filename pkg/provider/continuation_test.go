package provider

import "testing"

func TestEndsWithAssistantTurn(t *testing.T) {
	call := Message{Role: MessageRoleAssistant, Content: []Content{ToolCallContent(ToolCall{ID: "call_1", Name: "check", Arguments: "{}"})}}
	result := Message{Role: MessageRoleUser, Content: []Content{ToolResultContent(ToolResult{ID: "call_1", Parts: []Part{{Text: "OK"}}})}}
	for _, tc := range []struct {
		name     string
		messages []Message
		want     bool
	}{
		{"empty", nil, false},
		{"user turn", []Message{UserMessage("go")}, false},
		{"assistant text", []Message{UserMessage("go"), AssistantMessage("partial")}, true},
		{"assistant reasoning", []Message{UserMessage("go"), {Role: MessageRoleAssistant, Content: []Content{ReasoningContent(Reasoning{Summary: TextParts("thinking")})}}}, true},
		// A pending tool call resumes through its result, not through a new user turn.
		{"pending tool call", []Message{UserMessage("go"), call}, false},
		{"text then pending tool call", []Message{UserMessage("go"), AssistantMessage("checking"), call}, false},
		{"pending tool call then text", []Message{UserMessage("go"), call, AssistantMessage("checking")}, false},
		{"pending tool call, instructions, then text", []Message{UserMessage("go"), call, {Role: MessageRoleSystem, Content: []Content{TextContent("be brief")}}, AssistantMessage("checking")}, false},
		{"tool result", []Message{UserMessage("go"), call, result}, false},
		{"answer after tool result", []Message{UserMessage("go"), call, result, AssistantMessage("done")}, true},
		{"empty assistant after user", []Message{UserMessage("go"), {Role: MessageRoleAssistant}}, false},
		{"trailing instructions", []Message{UserMessage("go"), AssistantMessage("partial"), {Role: MessageRoleSystem, Content: []Content{TextContent("be brief")}}}, true},
	} {
		if got := EndsWithAssistantTurn(tc.messages); got != tc.want {
			t.Errorf("%s: EndsWithAssistantTurn = %t, want %t", tc.name, got, tc.want)
		}
	}
}
