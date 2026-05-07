package provider

import "strings"

type CompletionAccumulator struct {
	id    string
	model string

	status CompletionStatus

	role MessageRole

	content strings.Builder
	refusal strings.Builder

	reasonings  []Reasoning
	compactions []Compaction

	toolCalls      []ToolCall
	lastToolCallID string

	usage *Usage

	contentOrder []accumulatedContentRef
}

type accumulatedContentKind int

const (
	accumulatedContentReasoning accumulatedContentKind = iota
	accumulatedContentCompaction
	accumulatedContentText
	accumulatedContentRefusal
	accumulatedContentToolCall
)

type accumulatedContentRef struct {
	kind  accumulatedContentKind
	index int
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
				if a.content.Len() == 0 {
					a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentText})
				}

				a.content.WriteString(c.Text)
			}

			if c.Refusal != "" {
				if a.refusal.Len() == 0 {
					a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentRefusal})
				}

				a.refusal.WriteString(c.Refusal)
			}

			if c.Reasoning != nil {
				a.addReasoning(c.Reasoning)
			}

			if c.Compaction != nil {
				a.addCompaction(c.Compaction)
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
						a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentToolCall, index: len(a.toolCalls) - 1})
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
		if c.Usage.CacheReadInputTokens > a.usage.CacheReadInputTokens {
			a.usage.CacheReadInputTokens = c.Usage.CacheReadInputTokens
		}
		if c.Usage.CacheCreationInputTokens > a.usage.CacheCreationInputTokens {
			a.usage.CacheCreationInputTokens = c.Usage.CacheCreationInputTokens
		}
	}
}

// Distinct IDs are kept as separate entries; without an ID, deltas merge into
// the last entry. Collapsing distinct IDs would pair one item's ID with
// another's encrypted_content, which OpenAI rejects on the next turn.
func (a *CompletionAccumulator) addReasoning(r *Reasoning) {
	var target *Reasoning

	if r.ID != "" {
		for i := range a.reasonings {
			if a.reasonings[i].ID == r.ID {
				target = &a.reasonings[i]
				break
			}
		}
	} else if len(a.reasonings) > 0 {
		target = &a.reasonings[len(a.reasonings)-1]
	}

	if target == nil {
		a.reasonings = append(a.reasonings, Reasoning{ID: r.ID})
		a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentReasoning, index: len(a.reasonings) - 1})
		target = &a.reasonings[len(a.reasonings)-1]
	}

	target.Text += r.Text
	target.Summary += r.Summary

	if r.Signature != "" {
		target.Signature = r.Signature
	}
}

func (a *CompletionAccumulator) addCompaction(c *Compaction) {
	if c == nil || c.Signature == "" {
		return
	}

	if c.ID != "" {
		for i := range a.compactions {
			if a.compactions[i].ID == c.ID {
				a.compactions[i].Signature = c.Signature
				return
			}
		}
	} else if len(a.compactions) > 0 && a.compactions[len(a.compactions)-1].ID == "" {
		a.compactions[len(a.compactions)-1].Signature += c.Signature
		return
	}

	a.compactions = append(a.compactions, *c)
	a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentCompaction, index: len(a.compactions) - 1})
}

func (a *CompletionAccumulator) Result() *Completion {
	var content []Content

	for _, ref := range a.contentOrder {
		switch ref.kind {
		case accumulatedContentReasoning:
			content = append(content, ReasoningContent(a.reasonings[ref.index]))

		case accumulatedContentCompaction:
			content = append(content, CompactionContent(a.compactions[ref.index]))

		case accumulatedContentText:
			content = append(content, TextContent(a.content.String()))

		case accumulatedContentRefusal:
			content = append(content, RefusalContent(a.refusal.String()))

		case accumulatedContentToolCall:
			content = append(content, ToolCallContent(a.toolCalls[ref.index]))
		}
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
