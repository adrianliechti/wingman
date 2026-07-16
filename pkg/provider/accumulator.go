package provider

import "strings"

type CompletionAccumulator struct {
	id    string
	model string

	status       CompletionStatus
	stopDetails  *StopDetails
	stopSequence string

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

// NormalizeToolCallArguments returns well-formed arguments for a finalized
// tool call. A function call that streamed no argument bytes must surface as
// "{}" — empty strings fail JSON parsing in clients and tool runtimes.
// Custom (grammar) tools take free-form text where empty input is valid.
func NormalizeToolCallArguments(call ToolCall) ToolCall {
	if call.Arguments == "" && call.Kind != ToolKindCustom {
		call.Arguments = "{}"
	}

	return call
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

	if c.StopDetails != nil {
		a.stopDetails = c.StopDetails
	}

	if c.StopSequence != "" {
		a.stopSequence = c.StopSequence
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
				a.addToolCall(c.ToolCall)
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
		if c.Usage.ReasoningTokens > a.usage.ReasoningTokens {
			a.usage.ReasoningTokens = c.Usage.ReasoningTokens
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
	if r.Redacted {
		a.reasonings = append(a.reasonings, Reasoning{ID: r.ID, Signature: r.Signature, Redacted: true})
		a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentReasoning, index: len(a.reasonings) - 1})
		return
	}

	var target *Reasoning

	if r.ID != "" {
		for i := range a.reasonings {
			if a.reasonings[i].ID == r.ID {
				target = &a.reasonings[i]
				break
			}
		}
	} else if len(a.reasonings) > 0 {
		// A signed or redacted entry is complete; ID-less deltas start a new one
		if last := &a.reasonings[len(a.reasonings)-1]; !last.Redacted && last.Signature == "" {
			target = last
		}
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
	if c == nil || (c.ID == "" && c.Content == "" && c.Signature == "") {
		return
	}

	var target *Compaction

	if c.ID != "" {
		for i := range a.compactions {
			if a.compactions[i].ID == c.ID {
				target = &a.compactions[i]
				break
			}
		}
	} else if len(a.compactions) > 0 && a.compactions[len(a.compactions)-1].ID == "" {
		target = &a.compactions[len(a.compactions)-1]
	}

	if target == nil {
		a.compactions = append(a.compactions, Compaction{ID: c.ID})
		a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentCompaction, index: len(a.compactions) - 1})
		target = &a.compactions[len(a.compactions)-1]
	}

	if c.Content != "" {
		target.Content = c.Content
	}

	if c.Signature != "" {
		target.Signature = c.Signature
	}
}

// Deltas without an ID merge into the most recently seen call.
func (a *CompletionAccumulator) addToolCall(c *ToolCall) {
	targetID := c.ID

	if targetID == "" {
		targetID = a.lastToolCallID
	}

	if targetID == "" {
		return
	}

	var target *ToolCall

	for i := range a.toolCalls {
		if a.toolCalls[i].ID == targetID {
			target = &a.toolCalls[i]
			break
		}
	}

	if target == nil {
		if c.ID == "" {
			return
		}

		a.toolCalls = append(a.toolCalls, ToolCall{ID: c.ID})
		a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentToolCall, index: len(a.toolCalls) - 1})
		target = &a.toolCalls[len(a.toolCalls)-1]
	}

	a.lastToolCallID = targetID

	if c.Kind != "" {
		target.Kind = c.Kind
	}

	if c.Name != "" {
		target.Name = c.Name
	}

	if c.Namespace != "" {
		target.Namespace = c.Namespace
	}

	if c.Execution != "" {
		target.Execution = c.Execution
	}

	target.Arguments += c.Arguments
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
			call := a.toolCalls[ref.index]

			// A truncated call keeps its empty arguments — fabricating "{}"
			// would disguise an aborted call as a valid zero-argument one.
			if a.status != CompletionStatusIncomplete {
				call = NormalizeToolCallArguments(call)
			}

			content = append(content, ToolCallContent(call))
		}
	}

	return &Completion{
		ID:    a.id,
		Model: a.model,

		Status:       a.status,
		StopDetails:  a.stopDetails,
		StopSequence: a.stopSequence,

		Message: &Message{
			Role:    a.role,
			Content: content,
		},

		Usage: a.usage,
	}
}
