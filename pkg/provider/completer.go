package provider

import (
	"context"
	"strings"
)

type Completer interface {
	Complete(ctx context.Context, messages []Message, options *CompleteOptions) (*Completion, error)
}

type Message struct {
	Role MessageRole

	Content MessageContent

	Files []File

	Tool      string
	ToolCalls []ToolCall
}

func SystemMessage(text string) Message {
	return Message{
		Role: MessageRoleSystem,

		Content: MessageContent{
			{
				Text: &TextContent{
					Text: text,
				},
			},
		},
	}
}

func UserMessage(text string) Message {
	return Message{
		Role: MessageRoleUser,

		Content: MessageContent{
			{
				Text: &TextContent{
					Text: text,
				},
			},
		},
	}
}

func ToolMessage(id string, content string) Message {
	return Message{
		Role: MessageRoleTool,

		Tool: id,

		Content: MessageContent{
			{
				Text: &TextContent{
					Text: content,
				},
			},
		},
	}
}

func AssistantMessage(content string) Message {
	return Message{
		Role: MessageRoleAssistant,

		Content: MessageContent{
			{
				Text: &TextContent{
					Text: content,
				},
			},
		},
	}
}

type MessageContent []Content

func (c MessageContent) String() string {
	var parts []string

	for _, content := range c {
		if content.Text != nil {
			parts = append(parts, content.Text.Text)
		}
	}

	return strings.Join(parts, "\n\n")
}

// func MessageText(text string) MessageContent {
// 	return MessageContent{
// 		{
// 			Text: &TextContent{
// 				Text: text,
// 			},
// 		},
// 	}
// }

type Content struct {
	Text    *TextContent
	Refusal *RefusalContent

	File  *FileContent
	Image *ImageContent
}

type TextContent struct {
	Text string
}

type RefusalContent struct {
	Refusal string
}

type FileContent struct {
	FileName string
	FileData string
}

type ImageContent struct {
	ImageURL string
}

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

type ToolCall struct {
	ID string

	Name      string
	Arguments string
}

type StreamHandler = func(ctx context.Context, completion Completion) error

type CompleteOptions struct {
	Stream StreamHandler

	Effort ReasoningEffort

	Stop  []string
	Tools []Tool

	MaxTokens   *int
	Temperature *float32

	Format CompletionFormat
	Schema *Schema
}

type Completion struct {
	ID string

	Reason CompletionReason

	Message *Message

	Usage *Usage
}

type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
)

type CompletionFormat string

const (
	CompletionFormatJSON CompletionFormat = "json"
)

type CompletionReason string

const (
	CompletionReasonStop   CompletionReason = "stop"
	CompletionReasonLength CompletionReason = "length"
	CompletionReasonTool   CompletionReason = "tool"
	CompletionReasonFilter CompletionReason = "filter"
)
