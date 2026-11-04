package chat

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"
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

	Stream        bool                         `json:"stream,omitzero"`
	StreamOptions *ChatCompletionStreamOptions `json:"stream_options,omitzero"`

	MaxCompletionTokens *int `json:"max_completion_tokens,omitzero"`
	MaxTokens           *int `json:"max_tokens,omitzero"` // deprecated alias of max_completion_tokens
	Stop                any  `json:"stop,omitzero"`

	ReasoningEffort ReasoningEffort               `json:"reasoning_effort,omitzero"`
	Verbosity       Verbosity                     `json:"verbosity,omitzero"`
	ResponseFormat  *ChatCompletionResponseFormat `json:"response_format,omitzero"`

	Tools             []Tool      `json:"tools,omitempty"`
	ToolChoice        *ToolChoice `json:"tool_choice,omitzero"`
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitzero"`

	PromptCacheKey       *string             `json:"prompt_cache_key,omitzero"`
	PromptCacheRetention *string             `json:"prompt_cache_retention,omitzero"`
	PromptCacheOptions   *PromptCacheOptions `json:"prompt_cache_options,omitzero"`

	// frequency_penalty *float32
	// presence_penalty *float32
	// logit_bias
	// logprobs *bool
	// top_logprobs *int
	// n *int
	// seed *int
	// user string
}

type ReasoningEffort string

var (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
	ReasoningEffortMax     ReasoningEffort = "max"
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
	JSONSchema *Schema        `json:"json_schema,omitzero"`
}

type ChatCompletionStreamOptions struct {
	IncludeUsage *bool `json:"include_usage"`
}

type Schema struct {
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`

	Strict *bool `json:"strict,omitzero"`

	Schema map[string]any `json:"schema"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type ChatCompletion struct {
	ID                string  `json:"id"`
	Object            string  `json:"object"` // "chat.completion" | "chat.completion.chunk"
	Model             string  `json:"model"`
	SystemFingerprint *string `json:"system_fingerprint"`

	ServiceTier string `json:"service_tier,omitzero"`

	Created int64 `json:"created"`

	Choices []ChatCompletionChoice `json:"choices"`

	Usage *Usage `json:"usage,omitzero"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type ChatCompletionChoice struct {
	Index int `json:"index"`

	Delta   *ChatCompletionMessage `json:"delta,omitzero"`
	Message *ChatCompletionMessage `json:"message,omitzero"`

	FinishReason *FinishReason `json:"finish_reason"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type ChatCompletionMessage struct {
	Role MessageRole `json:"role,omitzero"`

	Content  *string          `json:"content,omitzero"`
	Contents []MessageContent `json:"-"`
	Refusal  *string          `json:"refusal,omitzero"`

	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitzero"`

	Annotations []any `json:"annotations,omitzero"`
}

type MessageContentType string

var (
	MessageContentTypeText    MessageContentType = "text"
	MessageContentTypeRefusal MessageContentType = "refusal"
	MessageContentTypeFile    MessageContentType = "file"
	MessageContentTypeImage   MessageContentType = "image_url"
	MessageContentTypeAudio   MessageContentType = "input_audio"
)

type MessageContent struct {
	Type MessageContentType `json:"type,omitzero"`

	Text    string `json:"text,omitzero"`
	Refusal string `json:"refusal,omitzero"`

	File  *MessageContentFile  `json:"file,omitzero"`
	Image *MessageContentImage `json:"image_url,omitzero"`
	Audio *MessageContentAudio `json:"input_audio,omitzero"`

	PromptCacheBreakpoint *PromptCacheBreakpoint `json:"prompt_cache_breakpoint,omitzero"`
}

type PromptCacheOptions = shared.PromptCacheOptions

// PromptCacheBreakpoint marks the end of a reusable prefix on a content part.
type PromptCacheBreakpoint struct {
	Mode string `json:"mode,omitzero"`
	TTL  string `json:"ttl,omitzero"`
}

type MessageContentImage struct {
	URL string `json:"url"`
}

type MessageContentFile struct {
	Name string `json:"filename,omitzero"`
	Data string `json:"file_data,omitzero"`
}

type MessageContentAudio struct {
	Data   string `json:"data,omitzero"`
	Format string `json:"format,omitzero"`
}

func (m *ChatCompletionMessage) MarshalJSONTo(enc *jsontext.Encoder) error {
	if m.Content != nil && m.Contents != nil {
		return errors.New("cannot have both content and contents")
	}

	type message ChatCompletionMessage
	if m.Contents != nil {
		return json.MarshalEncode(enc, struct {
			*message
			Content []MessageContent `json:"content"`
		}{(*message)(m), m.Contents})
	}

	return json.MarshalEncode(enc, struct {
		*message
		Refusal *string `json:"refusal"`
	}{(*message)(m), m.Refusal})
}

func (m *ChatCompletionMessage) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Role        MessageRole    `json:"role"`
		Content     jsontext.Value `json:"content"`
		Refusal     *string        `json:"refusal"`
		ToolCalls   []ToolCall     `json:"tool_calls"`
		ToolCallID  string         `json:"tool_call_id"`
		Annotations []any          `json:"annotations"`
	}
	if err := json.UnmarshalDecode(dec, &wire, jsonutil.PreserveNumbers); err != nil {
		return err
	}
	value := ChatCompletionMessage{Role: wire.Role, Refusal: wire.Refusal, ToolCalls: wire.ToolCalls, ToolCallID: wire.ToolCallID, Annotations: wire.Annotations}
	switch wire.Content.Kind() {
	case 0, 'n':
	case '"':
		if err := json.Unmarshal(wire.Content, &value.Content, dec.Options()); err != nil {
			return err
		}
	case '[':
		if err := json.Unmarshal(wire.Content, &value.Contents, dec.Options()); err != nil {
			return err
		}
	default:
		return errors.New("message content must be a string, an array, or null")
	}
	*m = value
	return nil
}

type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode,omitzero"`

	AllowedTools []ToolChoiceAllowedTool `json:"allowed_tools,omitempty"`
}

type ToolChoiceMode string

const (
	ToolChoiceModeNone     ToolChoiceMode = "none"
	ToolChoiceModeAuto     ToolChoiceMode = "auto"
	ToolChoiceModeRequired ToolChoiceMode = "required"
)

type ToolChoiceAllowedTool struct {
	Type     string                         `json:"type"`
	Function *ToolChoiceAllowedToolFunction `json:"function,omitzero"`
}

type ToolChoiceAllowedToolFunction struct {
	Name string `json:"name"`
}

func (t *ToolChoice) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	if dec.PeekKind() == '"' || dec.PeekKind() == 'n' {
		var mode ToolChoiceMode
		if err := json.UnmarshalDecode(dec, &mode); err != nil {
			return err
		}
		*t = ToolChoice{Mode: mode}
		return nil
	}
	var wire struct {
		Type         string                         `json:"type"`
		Function     *ToolChoiceAllowedToolFunction `json:"function"`
		Mode         ToolChoiceMode                 `json:"mode"`
		AllowedTools jsontext.Value                 `json:"allowed_tools"`
	}
	if err := json.UnmarshalDecode(dec, &wire); err != nil {
		return err
	}
	value := ToolChoice{Mode: wire.Mode}
	if wire.Type == "function" && wire.Function != nil {
		value.Mode = ToolChoiceModeRequired
		value.AllowedTools = []ToolChoiceAllowedTool{{Type: wire.Type, Function: wire.Function}}
	} else if wire.AllowedTools.Kind() == '{' {
		var allowed struct {
			Mode  ToolChoiceMode          `json:"mode"`
			Tools []ToolChoiceAllowedTool `json:"tools"`
		}
		if err := json.Unmarshal(wire.AllowedTools, &allowed, dec.Options()); err != nil {
			return err
		}
		value.Mode, value.AllowedTools = allowed.Mode, allowed.Tools
	} else if len(wire.AllowedTools) > 0 {
		if err := json.Unmarshal(wire.AllowedTools, &value.AllowedTools, dec.Options()); err != nil {
			return err
		}
	}
	*t = value
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
	ID string `json:"id,omitzero"`

	Type ToolType `json:"type,omitzero"`

	Index *int `json:"index,omitzero"`

	Function *FunctionCall `json:"function,omitzero"`
}

type Function struct {
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`

	Strict *bool `json:"strict,omitzero"`

	Parameters map[string]any `json:"parameters"`
}

// https://platform.openai.com/docs/api-reference/chat/object
type FunctionCall struct {
	Name      string `json:"name,omitzero"`
	Arguments string `json:"arguments"`
}

type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens,omitzero"`
	CompletionTokens        int                      `json:"completion_tokens,omitzero"`
	TotalTokens             int                      `json:"total_tokens,omitzero"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitzero"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitzero"`
}

type PromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens,omitzero"`
}

type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

func reasoningTokenDetails(usage *provider.Usage) *CompletionTokensDetails {
	if !usage.HasReasoningTokens() {
		return nil
	}
	return &CompletionTokensDetails{ReasoningTokens: *usage.ReasoningTokens}
}
