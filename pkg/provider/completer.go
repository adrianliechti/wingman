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
					ID:    id,
					Parts: []Part{{Text: content}},
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

type Content struct {
	Text    string
	Refusal string

	File *File

	Reasoning  *Reasoning
	Compaction *Compaction

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

type ReasoningOptions struct {
	Effort Effort

	IncludeSummary   bool
	IncludeSignature bool
}

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

type Completion struct {
	ID string

	Model  string
	Status CompletionStatus

	StopDetails *StopDetails

	Message *Message

	Usage *Usage
}

type StopDetails struct {
	Type string

	Category    string
	Explanation string
}

type Effort string

const (
	EffortNone    Effort = "none"
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"

	EffortAdaptive Effort = "adaptive"
)

// EffortFromBudget maps a numeric thinking-token budget to the coarser Effort
// scale, for providers that express reasoning as a token allowance rather than
// a level. A nil or negative budget means "let the model decide" (medium); 0
// disables thinking.
func EffortFromBudget(budget *int) Effort {
	if budget == nil {
		return EffortMedium
	}
	switch {
	case *budget < 0:
		return EffortMedium
	case *budget == 0:
		return EffortNone
	case *budget <= 1024:
		return EffortMinimal
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
}

type Compaction struct {
	ID string

	Content string

	Signature string
}

type CompactionOptions struct {
	Threshold int
}
