package chat

import (
	"testing"
)

func TestToolMessageWithStringContent(t *testing.T) {
	text := "hello"
	messages, err := toMessages([]ChatCompletionMessage{
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_1",
			Content:    &text,
		},
	})
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("expected 1 message with 1 content, got %+v", messages)
	}
	tr := messages[0].Content[0].ToolResult
	if tr == nil || tr.ID != "call_1" {
		t.Fatalf("expected ToolResult call_1, got %+v", messages[0].Content[0])
	}
	if len(tr.Parts) != 1 || tr.Parts[0].Text != "hello" {
		t.Fatalf("expected single text part 'hello', got %+v", tr.Parts)
	}
}

func TestToolMessageWithRichContents(t *testing.T) {
	messages, err := toMessages([]ChatCompletionMessage{
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_2",
			Contents: []MessageContent{
				{Type: MessageContentTypeText, Text: "hello "},
				{Type: MessageContentTypeText, Text: "world"},
			},
		},
	})
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("expected 1 message with 1 content, got %+v", messages)
	}
	tr := messages[0].Content[0].ToolResult
	if tr == nil || tr.ID != "call_2" {
		t.Fatalf("expected ToolResult call_2, got %+v", messages[0].Content[0])
	}
	if len(tr.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(tr.Parts), tr.Parts)
	}
	if tr.Parts[0].Text != "hello " || tr.Parts[1].Text != "world" {
		t.Fatalf("unexpected parts: %+v", tr.Parts)
	}
}

func TestToolMessageVsRegularUserMessage(t *testing.T) {
	text := "regular text"
	messages, err := toMessages([]ChatCompletionMessage{
		{
			Role:    MessageRoleUser,
			Content: &text,
		},
	})
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("expected 1 message with 1 content, got %+v", messages)
	}
	if messages[0].Content[0].ToolResult != nil {
		t.Fatalf("expected text content, got ToolResult: %+v", messages[0].Content[0])
	}
	if messages[0].Content[0].Text != "regular text" {
		t.Fatalf("expected 'regular text', got %q", messages[0].Content[0].Text)
	}
}
