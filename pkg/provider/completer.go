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

func CompactionContent(val Compaction) Content {
	return Content{
		Compaction: &val,
	}
}

func TextEditorCallContent(val TextEditorCall) Content {
	return Content{
		TextEditorCall: &val,
	}
}

func TextEditorCallResultContent(val TextEditorCallResult) Content {
	return Content{
		TextEditorCallResult: &val,
	}
}

type Content struct {
	Text string

	File *File

	Reasoning  *Reasoning
	Compaction *Compaction

	ToolCall   *ToolCall
	ToolResult *ToolResult

	TextEditorCall       *TextEditorCall
	TextEditorCallResult *TextEditorCallResult
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

// TextEditorCall represents a text editor / apply_patch operation.
type TextEditorCall struct {
	ID     string
	CallID string

	Command   TextEditorCommand
	Path      string
	Content   string // file_text for create, new_str for str_replace/insert
	OldText   string // old_str for str_replace
	InsertLine int   // for insert command
	ViewRange []int  // for view command
}

type TextEditorCommand string

const (
	TextEditorCommandView       TextEditorCommand = "view"
	TextEditorCommandCreate     TextEditorCommand = "create"
	TextEditorCommandStrReplace TextEditorCommand = "str_replace"
	TextEditorCommandInsert     TextEditorCommand = "insert"
)

// TextEditorCallResult represents the result of a text editor operation.
type TextEditorCallResult struct {
	CallID string
	Output string
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

	TextEditorTool bool

	Schema *Schema
}

type CompletionStatus string

const (
	CompletionStatusCompleted  CompletionStatus = "completed"
	CompletionStatusIncomplete CompletionStatus = "incomplete"
	CompletionStatusFailed     CompletionStatus = "failed"
)

type Completion struct {
	ID string

	Model  string
	Status CompletionStatus

	Usage   *Usage
	Message *Message
}

type Effort string

const (
	EffortNone    Effort = "none"
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortMax     Effort = "max"
)

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

	Signature string
}

type CompactionOptions struct {
	Threshold int
}
