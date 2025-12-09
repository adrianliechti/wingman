package chat

import (
	"encoding/json"
)

type MessageRole string

var (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleDeveloper MessageRole = "developer"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

type ResponseFormat string

var (
	ResponseFormatText       ResponseFormat = "text"
	ResponseFormatJSONObject ResponseFormat = "json_object"
	ResponseFormatJSONSchema ResponseFormat = "json_schema"
)

// https://platform.openai.com/docs/api-reference/chat/object
type FinishReason string

var (
	FinishReasonStop   FinishReason = "stop"
	FinishReasonLength FinishReason = "length"

	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonContentFilter FinishReason = "content_filter"
)

// https://platform.openai.com/docs/api-reference/chat/create
type ChatCompletionRequest struct {
	Model string `json:"model"`

	Messages []ChatCompletionMessage `json:"messages"`

	ReasoningEffort ReasoningEffort `json:"reasoning_effort,omitempty"`
	Verbosity       Verbosity       `json:"verbosity,omitempty"`

	Stream bool   `json:"stream,omitempty"`
	Stop   any    `json:"stop,omitempty"`
	Tools  []Tool `json:"tools,omitempty"`

	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`

	ResponseFormat *ChatCompletionResponseFormat `json:"response_format,omitempty"`

	StreamOptions *ChatCompletionStreamOptions `json:"stream_options,omitempty"`

	// frequency_penalty *float32
	// presence_penalty *float32

	// logit_bias
	// logprobs *bool
	// top_logprobs *int

	// n *int

	// seed *int

	// top_p *float32

	// tool_choice string: none, auto

	// user string
}

type ReasoningEffort string

var (
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
)

type Verbosity string

var (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

// https://platform.openai.com/docs/api-reference/chat/create
type ChatCompletionResponseFormat struct {
	Type       ResponseFormat `json:"type"`
	JSONSchema *Schema        `json:"json_schema,omitempty"`
}

type ChatCompletionStreamOptions struct {
	IncludeUsage *bool `json:"include_usage"`
}

type Schema struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	Strict *bool `json:"strict,omitempty"`

	Schema map[string]any `json:"schema"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type ChatCompletion struct {
	Object string `json:"object"` // "chat.completion" | "chat.completion.chunk"

	ID string `json:"id"`

	Model   string `json:"model"`
	Created int64  `json:"created"`

	Choices []ChatCompletionChoice `json:"choices"`

	Usage *Usage `json:"usage"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type ChatCompletionChoice struct {
	Index int `json:"index"`

	Delta   *ChatCompletionMessage `json:"delta,omitempty"`
	Message *ChatCompletionMessage `json:"message,omitempty"`

	FinishReason *FinishReason `json:"finish_reason"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type ChatCompletionMessage struct {
	Role MessageRole `json:"role,omitempty"`

	Content []MessageContent `json:"content"`

	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type MessageContentType string

var (
	MessageContentTypeText  MessageContentType = "text"
	MessageContentTypeFile  MessageContentType = "file"
	MessageContentTypeImage MessageContentType = "image_url"
	MessageContentTypeAudio MessageContentType = "input_audio"
)

type MessageContent struct {
	Type MessageContentType `json:"type,omitempty"`

	Text string `json:"text,omitempty"`

	File  *MessageContentFile  `json:"file,omitempty"`
	Image *MessageContentImage `json:"image_url,omitempty"`
	Audio *MessageContentAudio `json:"input_audio,omitempty"`
}

type MessageContentImage struct {
	URL string `json:"url"`
}

type MessageContentFile struct {
	Name string `json:"filename,omitempty"`
	Data string `json:"file_data,omitempty"`
}

type MessageContentAudio struct {
	Data   string `json:"data,omitempty"`
	Format string `json:"format,omitempty"`
}

func (m *ChatCompletionMessage) MarshalJSON() ([]byte, error) {
	if len(m.Content) == 1 && m.Content[0].Type == MessageContentTypeText {
		type Message struct {
			Role MessageRole `json:"role,omitempty"`

			Content string `json:"content,omitempty"`

			ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
			ToolCallID string     `json:"tool_call_id,omitempty"`
		}

		val := &Message{
			Role: m.Role,

			Content: m.Content[0].Text,

			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		}

		return json.Marshal(val)
	} else {
		val := struct {
			Role MessageRole `json:"role,omitempty"`

			Content []MessageContent `json:"content,omitempty"`

			ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
			ToolCallID string     `json:"tool_call_id,omitempty"`
		}(*m)

		return json.Marshal(val)
	}
}

func (m *ChatCompletionMessage) UnmarshalJSON(data []byte) error {
	type1 := struct {
		Role MessageRole `json:"role,omitempty"`

		Content string `json:"content"`

		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}{}

	if err := json.Unmarshal(data, &type1); err == nil {
		*m = ChatCompletionMessage{
			Role: type1.Role,

			Content: []MessageContent{
				{
					Type: MessageContentTypeText,
					Text: type1.Content,
				},
			},

			ToolCalls:  type1.ToolCalls,
			ToolCallID: type1.ToolCallID,
		}

		return nil
	}

	type2 := struct {
		Role MessageRole `json:"role,omitempty"`

		Content []MessageContent `json:"content"`

		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}{}

	if err := json.Unmarshal(data, &type2); err == nil {
		*m = ChatCompletionMessage(type2)
		return err
	}

	return nil
}

// https://platform.openai.com/docs/api-reference/chat/object
type ToolType string

var (
	ToolTypeFunction ToolType = "function"
)

type Tool struct {
	Type ToolType `json:"type"`

	ToolFunction *Function `json:"function"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type ToolCall struct {
	ID string `json:"id,omitempty"`

	Type ToolType `json:"type,omitempty"`

	Index int `json:"index"`

	Function *FunctionCall `json:"function,omitempty"`
}

type Function struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	Strict *bool `json:"strict,omitempty"`

	Parameters map[string]any `json:"parameters"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}
