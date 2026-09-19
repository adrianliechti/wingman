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

	// Phase labels an assistant message item (commentary or final answer) in
	// replayed history and in SplitMessages results. Streamed parts carry
	// their phase on Content instead.
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

func InstructionsContent(val Instructions) Content {
	return Content{Instructions: &val}
}

type Content struct {
	// MessageID identifies the assistant message item a text or refusal part
	// belongs to, the way Reasoning.ID and ToolCall.ID identify theirs. In a
	// stream, a part with a new ID starts a new item and ID-less parts join
	// the current one; an accumulated result keeps the IDs so SplitMessages
	// can restore the items. Phase is metadata of the item and may be empty.
	MessageID string
	Phase     MessagePhase

	// CacheControl marks the end of a reusable prompt prefix. Providers honor
	// it in explicit cache mode; with implicit caching their automatic prefix
	// cache already covers everything before it.
	CacheControl *CacheControl

	Text    string
	Refusal string

	File *File

	Reasoning  *Reasoning
	Compaction *Compaction

	CompactionTrigger   bool
	ConfigurationUpdate *ConfigurationUpdate
	Instructions        *Instructions

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
	CacheOptions      *CacheOptions

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

	// Reasoning is the effective context mode reported by the provider.
	Reasoning ReasoningContext

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

// CacheOptions refines the prompt caching a provider applies by default
// wherever its backend offers it. Key groups requests that share a prefix on
// backends that route caches by key, and Retention asks to keep cached
// prefixes longer than the backend's default where that is available.
// Neither turns caching on or off.
type CacheOptions struct {
	Key       string
	Retention CacheRetention

	// Mode selects how the prefix is cached: implicit, the default, lets the
	// provider cache the stable prefix on its own; explicit caches only at
	// the parts marked with a CacheControl.
	Mode CacheMode
}

type CacheRetention string

const (
	CacheRetentionDefault  CacheRetention = ""
	CacheRetentionExtended CacheRetention = "extended"
)

type CacheMode string

const (
	CacheModeImplicit CacheMode = ""
	CacheModeExplicit CacheMode = "explicit"
)

// CacheControl marks a content part as a cache breakpoint. Retention
// overrides the request's retention for this breakpoint where the backend
// allows it.
type CacheControl struct {
	Retention CacheRetention
}

// SplitMessages restores the message items of an accumulated assistant
// response. A text or refusal part whose MessageID differs from the current
// item's starts a new item. Reasoning and tool calls stay with the item they
// were streamed with. Messages without identified parts come back as is.
func (m Message) SplitMessages() []Message {
	var messages []Message

	current := Message{Role: m.Role, Phase: m.Phase}
	currentID := ""
	filled := false

	for _, content := range m.Content {
		part := content.Text != "" || content.Refusal != ""

		if part && content.MessageID != "" && content.MessageID != currentID {
			if filled {
				messages = append(messages, current)
				current = Message{Role: m.Role}
				filled = false
			}

			currentID = content.MessageID
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
