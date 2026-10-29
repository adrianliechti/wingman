package provider

import "strings"

type CompletionAccumulator struct {
	id    string
	model string

	tier             Tier
	reasoningContext ReasoningContext

	status       CompletionStatus
	stopReason   StopReason
	stopDetails  *StopDetails
	stopSequence string

	role     MessageRole
	messages []*accumulatedMessage

	reasonings  []*accumulatedReasoning
	compactions []Compaction

	toolCall    string
	toolCalls   []ToolCall
	toolResults []ToolResult

	contentOrder []accumulatedContentRef

	usage *Usage
}

type accumulatedMessage struct {
	id    string
	phase MessagePhase

	hasText bool

	text    strings.Builder
	refusal strings.Builder
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
	accumulatedContentToolResult
)

type accumulatedContentRef struct {
	kind  accumulatedContentKind
	index int
}

func (a *CompletionAccumulator) Add(c Completion) {
	if c.ID != "" {
		a.id = c.ID
	}

	if c.Tier != "" {
		a.tier = c.Tier
	}

	if c.Model != "" {
		a.model = c.Model
	}

	if c.Reasoning != "" {
		a.reasoningContext = c.Reasoning
	}

	if c.Status != "" {
		a.status = c.Status
	}

	if c.StopReason != "" {
		a.stopReason = c.StopReason
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
			if c.MessageID != "" {
				a.beginMessage(c.MessageID, c.Phase)
			}

			if c.Text != "" || c.TextPresent {
				message := a.currentMessage()

				if !message.hasText {
					a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentText, index: len(a.messages) - 1})
					message.hasText = true
				}

				message.text.WriteString(c.Text)
			}

			if c.Refusal != "" {
				message := a.currentMessage()

				if message.refusal.Len() == 0 {
					a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentRefusal, index: len(a.messages) - 1})
				}

				message.refusal.WriteString(c.Refusal)
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
			if c.ToolResult != nil {
				a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentToolResult, index: len(a.toolResults)})
				a.toolResults = append(a.toolResults, *c.ToolResult)
			}
		}
	}

	if c.Usage != nil {
		if a.usage == nil {
			a.usage = &Usage{}
		}

		a.usage.Merge(c.Usage)
	}
}

// beginMessage makes the item with the given ID current. A new ID starts an
// item, reusing the current one while it holds no text or refusal, so that
// reasoning and tool calls streamed before the first part stay with it.
func (a *CompletionAccumulator) beginMessage(id string, phase MessagePhase) {
	n := len(a.messages)

	if n > 0 && a.messages[n-1].id == id {
		if phase != "" {
			a.messages[n-1].phase = phase
		}

		return
	}

	if n == 0 || a.messages[n-1].hasText || a.messages[n-1].refusal.Len() > 0 {
		a.messages = append(a.messages, &accumulatedMessage{})
	}

	message := a.messages[len(a.messages)-1]
	message.id = id
	message.phase = phase
}

func (a *CompletionAccumulator) currentMessage() *accumulatedMessage {
	if len(a.messages) == 0 {
		a.messages = append(a.messages, &accumulatedMessage{})
	}

	return a.messages[len(a.messages)-1]
}

type accumulatedReasoning struct {
	Reasoning
	content, summary PartAccumulator
}

func (r *accumulatedReasoning) result() Reasoning {
	result := r.Reasoning
	result.Content, result.Summary = r.content.Parts(), r.summary.Parts()
	return result
}

// Distinct IDs and signed blocks retain separate replay state.
func (a *CompletionAccumulator) addReasoning(r *Reasoning) {
	var target *accumulatedReasoning
	if !r.Redacted {
		if r.ID != "" {
			for _, entry := range a.reasonings {
				if entry.ID == r.ID {
					target = entry
					break
				}
			}
		} else if len(a.reasonings) > 0 && len(a.contentOrder) > 0 && a.contentOrder[len(a.contentOrder)-1].kind == accumulatedContentReasoning {
			last := a.reasonings[len(a.reasonings)-1]
			if !last.Redacted && last.Signature == "" {
				target = last
			}
		}
	}
	if target == nil {
		target = &accumulatedReasoning{Reasoning: Reasoning{ID: r.ID, Redacted: r.Redacted}}
		a.reasonings = append(a.reasonings, target)
		a.contentOrder = append(a.contentOrder, accumulatedContentRef{kind: accumulatedContentReasoning, index: len(a.reasonings) - 1})
	}
	for _, part := range r.Content {
		target.content.Add(part)
	}
	for _, part := range r.Summary {
		target.summary.Add(part)
	}
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
		targetID = a.toolCall
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

	a.toolCall = targetID

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

	if c.Async {
		target.Async = true
	}

	target.Arguments += c.Arguments
}

func (a *CompletionAccumulator) Result() *Completion {
	var content []Content

	for _, ref := range a.contentOrder {
		switch ref.kind {
		case accumulatedContentReasoning:
			content = append(content, ReasoningContent(a.reasonings[ref.index].result()))

		case accumulatedContentCompaction:
			content = append(content, CompactionContent(a.compactions[ref.index]))

		case accumulatedContentText:
			m := a.messages[ref.index]
			content = append(content, Content{MessageID: m.id, Phase: m.phase, Text: m.text.String(), TextPresent: m.text.Len() == 0})

		case accumulatedContentRefusal:
			m := a.messages[ref.index]
			content = append(content, Content{MessageID: m.id, Phase: m.phase, Refusal: m.refusal.String()})

		case accumulatedContentToolCall:
			call := a.toolCalls[ref.index]

			// A truncated call keeps its empty arguments — fabricating "{}"
			// would disguise an aborted call as a valid zero-argument one.
			if a.status != CompletionStatusIncomplete && a.status != CompletionStatusFailed && a.status != CompletionStatusRefused {
				call = NormalizeToolCallArguments(call)
			}

			content = append(content, ToolCallContent(call))
		case accumulatedContentToolResult:
			content = append(content, ToolResultContent(a.toolResults[ref.index]))
		}
	}

	return &Completion{
		ID:        a.id,
		Model:     a.model,
		Reasoning: a.reasoningContext,
		Tier:      a.tier,

		Status:       a.status,
		StopReason:   a.stopReason,
		StopDetails:  a.stopDetails,
		StopSequence: a.stopSequence,

		Message: &Message{
			Role:    a.role,
			Content: content,
		},

		Usage: a.usage,
	}
}
