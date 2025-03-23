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
				Text1: text,
			},
		},
	}
}

func UserMessage(text string) Message {
	return Message{
		Role: MessageRoleUser,

		Content: MessageContent{
			{
				Text1: text,
			},
		},
	}
}

func AssistantMessage(content string) Message {
	return Message{
		Role: MessageRoleAssistant,

		Content: MessageContent{
			{
				Text1: content,
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
				Text1: content,
			},
		},
	}
}

type MessageContent []Content

func (c MessageContent) String() string {
	var parts []string

	for _, content := range c {
		if content.Text1 != "" {
			parts = append(parts, content.Text1)
		}
	}

	return strings.Join(parts, "\n\n")
}

type CompletionAccumulator struct {
	id string

	role   MessageRole
	reason CompletionReason

	content strings.Builder
	refusal strings.Builder

	toolCalls []ToolCall

	usage *Usage
}

func (a *CompletionAccumulator) Add(c Completion) {
	if c.ID != "" {
		a.id = c.ID
	}

	if c.Reason != "" {
		a.reason = c.Reason
	}

	if c.Message != nil {
		if c.Message.Role != "" {
			a.role = c.Message.Role
		}

		for _, c := range c.Message.Content {
			if c.Text1 != "" {
				a.content.WriteString(c.Text1)
			}

			if c.Refusal1 != "" {
				a.refusal.WriteString(c.Refusal1)
			}
		}

		for _, c := range c.Message.ToolCalls {
			if c.ID != "" {
				a.toolCalls = append(a.toolCalls, ToolCall{
					ID: c.ID,
				})
			}

			if len(a.toolCalls) == 0 {
				// TODO: Error Handling
				continue
			}

			a.toolCalls[len(a.toolCalls)-1].Name += c.Name
			a.toolCalls[len(a.toolCalls)-1].Arguments += c.Arguments
		}
	}

	if c.Usage != nil {
		if a.usage == nil {
			a.usage = &Usage{}
		}

		a.usage.InputTokens += c.Usage.InputTokens
		a.usage.OutputTokens += c.Usage.OutputTokens
	}
}

func (a *CompletionAccumulator) Result() *Completion {
	var content MessageContent

	if a.content.Len() > 0 {
		content = append(content, TextContent(a.content.String()))
	}

	if a.refusal.Len() > 0 {
		content = append(content, RefusalContent(a.refusal.String()))
	}

	return &Completion{
		ID: a.id,

		Reason: a.reason,

		Message: &Message{
			Role:    a.role,
			Content: content,

			ToolCalls: a.toolCalls,
		},

		Usage: a.usage,
	}
}

func TextContent(val string) Content {
	return Content{
		Text1: val,
	}
}

func RefusalContent(val string) Content {
	return Content{
		Refusal1: val,
	}
}

type Content struct {
	Text1    string
	Refusal1 string

	File  *FileContent
	Image *ImageContent
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
