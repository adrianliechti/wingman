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

	Content []Content
}

func SystemMessage(content string) Message {
	return Message{
		Role: MessageRoleSystem,

		Content: []Content{
			{
				Text: content,
			},
		},
	}
}

func UserMessage(content string) Message {
	return Message{
		Role: MessageRoleUser,

		Content: []Content{
			{
				Text: content,
			},
		},
	}
}

func AssistantMessage(content string) Message {
	return Message{
		Role: MessageRoleAssistant,

		Content: []Content{
			{
				Text: content,
			},
		},
	}
}

func ToolMessage(id, content string) Message {
	return Message{
		Role: MessageRoleUser,

		Content: []Content{
			{
				ToolResult: &ToolResult{
					ID:   id,
					Data: content,
				},
			},
		},
	}
}

func (m Message) Text() string {
	var parts []string

	for _, c := range m.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}

	return strings.Join(parts, "\n\n")
}

func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall

	for _, c := range m.Content {
		if c.ToolCall != nil {
			calls = append(calls, *c.ToolCall)
		}
	}

	return calls
}

func (m Message) ToolResult() (id string, content string, ok bool) {
	for _, c := range m.Content {
		if c.ToolResult != nil {
			return c.ToolResult.ID, c.ToolResult.Data, true
		}
	}

	return "", "", false
}

type CompletionAccumulator struct {
	id    string
	model string

	role MessageRole

	content strings.Builder

	toolCalls []ToolCall

	usage *Usage
}

func (a *CompletionAccumulator) Add(c Completion) {
	if c.ID != "" {
		a.id = c.ID
	}

	if c.Model != "" {
		a.model = c.Model
	}

	if c.Message != nil {
		if c.Message.Role != "" {
			a.role = c.Message.Role
		}

		for _, c := range c.Message.Content {
			if c.Text != "" {
				a.content.WriteString(c.Text)
			}

			if c.ToolCall != nil {
				if c.ToolCall.ID != "" {
					a.toolCalls = append(a.toolCalls, ToolCall{
						ID: c.ToolCall.ID,
					})
				}

				if len(a.toolCalls) == 0 {
					// TODO: Error Handling
					continue
				}

				a.toolCalls[len(a.toolCalls)-1].Name += c.ToolCall.Name
				a.toolCalls[len(a.toolCalls)-1].Arguments += c.ToolCall.Arguments
			}
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
	var content []Content

	if a.content.Len() > 0 {
		content = append(content, TextContent(a.content.String()))
	}

	for _, call := range a.toolCalls {
		content = append(content, ToolCallContent(call))
	}

	return &Completion{
		ID:    a.id,
		Model: a.model,

		Message: &Message{
			Role:    a.role,
			Content: content,
		},

		Usage: a.usage,
	}
}

func TextContent(val string) Content {
	return Content{
		Text: val,
	}
}

func FileContent(val *File) Content {
	return Content{
		File: val,
	}
}

func ToolCallContent(val ToolCall) Content {
	return Content{
		ToolCall: &val,
	}
}

func ToolResultContent(val ToolResult) Content {
	return Content{
		ToolResult: &val,
	}
}

type Content struct {
	Text string

	File *File

	ToolCall   *ToolCall
	ToolResult *ToolResult
}

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

type ToolCall struct {
	ID string

	Name      string
	Arguments string
}

type StreamHandler = func(ctx context.Context, completion Completion) error

type CompleteOptions struct {
	Stream StreamHandler

	Effort    Effort
	Verbosity Verbosity

	Stop  []string
	Tools []Tool

	MaxTokens   *int
	Temperature *float32

	Format CompletionFormat
	Schema *Schema
}

type Completion struct {
	ID    string
	Model string

	Message *Message

	Usage *Usage
}

type Effort string

const (
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
)

type Verbosity string

const (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

type CompletionFormat string

const (
	CompletionFormatJSON CompletionFormat = "json"
)
