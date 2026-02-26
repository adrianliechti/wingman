package provider

import (
	"context"
	"iter"
	"strings"
)

type Completer interface {
	Complete(ctx context.Context, messages []Message, options *CompleteOptions) iter.Seq2[*Completion, error]
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

	reasoning *Reasoning

	toolCalls      []ToolCall
	lastToolCallID string

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

			if c.Reasoning != nil {
				if a.reasoning == nil {
					a.reasoning = &Reasoning{}
				}

				if c.Reasoning.ID != "" {
					a.reasoning.ID = c.Reasoning.ID
				}

				a.reasoning.Text += c.Reasoning.Text
				a.reasoning.Summary += c.Reasoning.Summary

				if c.Reasoning.Signature != "" {
					a.reasoning.Signature = c.Reasoning.Signature
				}
			}

			if c.ToolCall != nil {
				// Only create a new tool call if we have an ID and haven't seen it before
				if c.ToolCall.ID != "" {
					found := false
					for i := range a.toolCalls {
						if a.toolCalls[i].ID == c.ToolCall.ID {
							found = true
							break
						}
					}
					if !found {
						a.toolCalls = append(a.toolCalls, ToolCall{
							ID: c.ToolCall.ID,
						})
					}
					a.lastToolCallID = c.ToolCall.ID
				}

				if len(a.toolCalls) == 0 {
					continue
				}

				toolCallIndex := -1
				targetID := c.ToolCall.ID

				if targetID == "" {
					targetID = a.lastToolCallID
				}

				for i := range a.toolCalls {
					if a.toolCalls[i].ID == targetID {
						toolCallIndex = i
						break
					}
				}

				if toolCallIndex == -1 {
					continue
				}

				if c.ToolCall.Name != "" {
					a.toolCalls[toolCallIndex].Name = c.ToolCall.Name
				}

				a.toolCalls[toolCallIndex].Arguments += c.ToolCall.Arguments
			}
		}
	}

	if c.Usage != nil {
		if a.usage == nil {
			a.usage = &Usage{}
		}

		if c.Usage.InputTokens > a.usage.InputTokens {
			a.usage.InputTokens = c.Usage.InputTokens
		}
		if c.Usage.OutputTokens > a.usage.OutputTokens {
			a.usage.OutputTokens = c.Usage.OutputTokens
		}
	}
}

func (a *CompletionAccumulator) Result() *Completion {
	var content []Content

	if a.reasoning != nil {
		content = append(content, ReasoningContent(*a.reasoning))
	}

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

func ReasoningContent(val Reasoning) Content {
	return Content{
		Reasoning: &val,
	}
}

type Content struct {
	Text string

	File *File

	Reasoning *Reasoning

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

type CompleteOptions struct {
	Effort    Effort
	Verbosity Verbosity

	Stop  []string
	Tools []Tool

	MaxTokens   *int
	Temperature *float32

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

type Reasoning struct {
	ID        string
	Text      string
	Summary   string
	Signature string
}
