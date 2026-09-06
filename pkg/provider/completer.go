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
	Role  MessageRole
	Phase MessagePhase

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
					ID:    id,
					Parts: []Part{{Text: content}},
				},
			},
		},
	}
}

// Text reads all text blocks of this message, including commentary.
// Use Completion.Text to select the final answer from an accumulated response.
func (m Message) Text() string {
	var parts []string

	for _, c := range m.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}

	return strings.Join(parts, "\n\n")
}

func (m Message) Refusal() string {
	var parts []string

	for _, c := range m.Content {
		if c.Refusal != "" {
			parts = append(parts, c.Refusal)
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

func (m Message) ToolResult() (*ToolResult, bool) {
	for _, c := range m.Content {
		if c.ToolResult != nil {
			return c.ToolResult, true
		}
	}

	return nil, false
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

func RefusalContent(val string) Content {
	return Content{
		Refusal: val,
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

func CompactionContent(val Compaction) Content {
	return Content{
		Compaction: &val,
	}
}

func CompactionTriggerContent() Content {
	return Content{CompactionTrigger: true}
}

func ConfigurationUpdateContent(val ConfigurationUpdate) Content {
	return Content{
		ConfigurationUpdate: &val,
	}
}

type Content struct {
	// Phase marks a text or refusal part as the content of one message item in
	// an accumulated result; every phased part is its own item. Parts without
	// a phase inherit the enclosing message.
	Phase MessagePhase

	Text    string
	Refusal string

	File *File

	Reasoning  *Reasoning
	Compaction *Compaction

	CompactionTrigger   bool
	ConfigurationUpdate *ConfigurationUpdate

	ToolCall   *ToolCall
	ToolResult *ToolResult
}

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

type MessagePhase string

const (
	MessagePhaseCommentary  MessagePhase = "commentary"
	MessagePhaseFinalAnswer MessagePhase = "final_answer"
)

type ToolCall struct {
	ID    string
	Async bool

	Kind ToolKind

	Name      string
	Namespace string

	Execution string

	Arguments string
}

type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceAny  ToolChoice = "any"
	ToolChoiceNone ToolChoice = "none"
)

type ToolOptions struct {
	Allowed []string

	Choice ToolChoice

	DisableParallelToolCalls bool
}

type OutputOptions struct {
	Verbosity Verbosity
}

// ReasoningOptions has two axes, mirroring the backends that separate them:
// Type switches thinking on (adaptive) or off, Effort sets how hard the model
// works. Type "" leaves the backend default; Effort "" leaves the level to the
// backend. An Effort without a Type raises effort on Claude without enabling
// thinking, and selects the reasoning level on OpenAI and Gemini, which have
// no separate switch.
type ReasoningOptions struct {
	Type ReasoningType

	Effort  Effort
	Context ReasoningContext

	// IncludeSummary asks for visible reasoning text; IncludeSignature asks
	// for the opaque state that lets the next turn continue the reasoning.
	IncludeSummary   bool
	IncludeSignature bool
}

type ReasoningContext string

const (
	ReasoningContextAuto        ReasoningContext = "auto"
	ReasoningContextCurrentTurn ReasoningContext = "current_turn"
	ReasoningContextAllTurns    ReasoningContext = "all_turns"
)

type CompleteOptions struct {
	Stop []string

	MaxTokens   *int
	Temperature *float32

	Tools       []Tool
	ToolOptions *ToolOptions

	OutputOptions     *OutputOptions
	ReasoningOptions  *ReasoningOptions
	CompactionOptions *CompactionOptions

	Schema *Schema
}

type CompletionStatus string

const (
	CompletionStatusCompleted  CompletionStatus = "completed"
	CompletionStatusIncomplete CompletionStatus = "incomplete"
	CompletionStatusFailed     CompletionStatus = "failed"
	CompletionStatusRefused    CompletionStatus = "refused"
)

// StopReason preserves the provider's native completion boundary when it has
// semantics that CompletionStatus alone cannot represent.
type StopReason string

const (
	StopReasonEndTurn         StopReason = "end_turn"
	StopReasonMaxTokens       StopReason = "max_tokens"
	StopReasonStopSequence    StopReason = "stop_sequence"
	StopReasonToolUse         StopReason = "tool_use"
	StopReasonPauseTurn       StopReason = "pause_turn"
	StopReasonCompaction      StopReason = "compaction"
	StopReasonRefusal         StopReason = "refusal"
	StopReasonContextExceeded StopReason = "context_exceeded"
)

type Completion struct {
	ID string

	Model  string
	Status CompletionStatus

	StopReason   StopReason
	StopDetails  *StopDetails
	StopSequence string

	Message *Message

	Usage *Usage
}

type StopDetails struct {
	Type string

	Category    string
	Explanation string
}

type ReasoningType string

const (
	ReasoningTypeDisabled ReasoningType = "disabled"
	ReasoningTypeAdaptive ReasoningType = "adaptive"
)

type Effort string

const (
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"
)

func EffortFromBudget(budget *int) Effort {
	if budget == nil {
		return EffortMedium
	}
	switch {
	case *budget < 0:
		return EffortMedium
	case *budget == 0:
		return ""
	case *budget <= 4096:
		return EffortLow
	case *budget <= 16384:
		return EffortMedium
	default:
		return EffortHigh
	}
}

type Verbosity string

const (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

type Reasoning struct {
	ID string

	Text    string
	Summary string

	Signature string

	// Redacted marks an encrypted reasoning item (e.g. Anthropic
	// redacted_thinking); Signature carries the opaque data blob.
	Redacted bool
}

type Compaction struct {
	ID string

	Content string

	Signature string
}

type ConfigurationUpdate struct {
	ReasoningEffort Effort
}

type CompactionOptions struct {
	Trigger   bool
	Threshold int
}

// SplitMessages restores the message items of an accumulated assistant
// response. A phased text or refusal part starts a new item when the current
// one already holds text or a refusal; reasoning and tool calls stay with the
// item they were streamed with. Messages without phased parts come back as is.
func (m Message) SplitMessages() []Message {
	var messages []Message

	current := Message{Role: m.Role, Phase: m.Phase}
	filled := false

	for _, content := range m.Content {
		part := content.Text != "" || content.Refusal != ""

		if part && content.Phase != "" {
			if filled {
				messages = append(messages, current)
				current = Message{Role: m.Role}
				filled = false
			}

			current.Phase = content.Phase
		}

		current.Content = append(current.Content, content)
		filled = filled || part
	}

	if len(current.Content) > 0 || len(messages) == 0 {
		messages = append(messages, current)
	}

	return messages
}
