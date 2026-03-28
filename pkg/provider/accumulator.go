package provider

import "strings"

type CompletionAccumulator struct {
	id    string
	model string

	status CompletionStatus

	role MessageRole

	content strings.Builder
	refusal strings.Builder

	reasoning  *Reasoning
	compaction *Compaction

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

	if c.Status != "" {
		a.status = c.Status
	}

	if c.Message != nil {
		if c.Message.Role != "" {
			a.role = c.Message.Role
		}

		for _, c := range c.Message.Content {
			if c.Text != "" {
				a.content.WriteString(c.Text)
			}

			if c.Refusal != "" {
				a.refusal.WriteString(c.Refusal)
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

			if c.Compaction != nil {
				a.compaction = c.Compaction
			}

			if c.ToolCall != nil {
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

	if a.compaction != nil {
		content = append(content, CompactionContent(*a.compaction))
	}

	if a.content.Len() > 0 {
		content = append(content, TextContent(a.content.String()))
	}

	if a.refusal.Len() > 0 {
		content = append(content, RefusalContent(a.refusal.String()))
	}

	for _, call := range a.toolCalls {
		content = append(content, ToolCallContent(call))
	}

	return &Completion{
		ID:    a.id,
		Model: a.model,

		Status: a.status,

		Message: &Message{
			Role:    a.role,
			Content: content,
		},

		Usage: a.usage,
	}
}
