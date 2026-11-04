package anthropic

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
)

// Request types

type MessageRequest struct {
	Model string `json:"model"`

	Messages []MessageParam `json:"messages"`
	System   any            `json:"system,omitzero"` // string or text blocks

	Stream bool `json:"stream,omitzero"`

	CacheControl *CacheControlParam `json:"cache_control,omitzero"`

	Speed         string   `json:"speed,omitzero"`
	MaxTokens     *int     `json:"max_tokens"`
	StopSequences []string `json:"stop_sequences,omitempty"`

	Tools      []ToolParam `json:"tools,omitempty"`
	ToolChoice *ToolChoice `json:"tool_choice,omitzero"`

	Thinking     *ThinkingConfig `json:"thinking,omitzero"`
	OutputConfig *OutputConfig   `json:"output_config,omitzero"`
	OutputFormat *OutputFormat   `json:"output_format,omitzero"`

	ContextManagement *ContextManagement `json:"context_management,omitzero"`
	Compaction        *CompactionConfig  `json:"compaction,omitzero"`
}

type CompactionConfig struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitzero"`
}

type ContextManagement struct {
	Edits []ContextManagementEdit `json:"edits,omitempty"`
}

type ContextManagementEdit struct {
	Keep                 jsontext.Value `json:"keep,omitzero"`
	Instructions         string         `json:"instructions,omitzero"`
	PauseAfterCompaction bool           `json:"pause_after_compaction,omitzero"`
	Type                 string         `json:"type"` // "compact_20260112"
	Trigger              *struct {
		Type  string `json:"type"` // "input_tokens"
		Value int    `json:"value"`
	} `json:"trigger,omitzero"`
}

type ThinkingConfig struct {
	Type         string         `json:"type"`                   // "enabled", "adaptive", "disabled", or "between_tools"
	BudgetTokens int            `json:"budget_tokens,omitzero"` // required when type is "enabled"
	Display      string         `json:"display,omitzero"`       // "summarized", "omitted", or "updates"
	BlockBinding jsontext.Value `json:"block_binding,omitzero"`
}

type OutputConfig struct {
	Effort     string         `json:"effort,omitzero"` // "low", "medium", "high", "xhigh", "max"
	Format     *OutputFormat  `json:"format,omitzero"`
	TaskBudget jsontext.Value `json:"task_budget,omitzero"`
}

type OutputFormat struct {
	Type   string         `json:"type"`             // "json_schema"
	Name   string         `json:"name,omitzero"`    // optional name for the schema
	Schema map[string]any `json:"schema,omitempty"` // JSON Schema definition
	Strict *bool          `json:"strict,omitzero"`  // whether to use strict mode
}

type MessageParam struct {
	Role         MessageRole   `json:"role"`
	Content      any           `json:"content"` // string or []ContentBlockParam
	OutputConfig *OutputConfig `json:"output_config,omitzero"`
	ClearAt      string        `json:"clear_at,omitzero"`
}

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

type ContentBlockParam struct {
	Type string `json:"type"`

	// For text blocks
	Text string `json:"text,omitzero"`

	// For image and document blocks
	Source       *BlockSource `json:"source,omitzero"`
	SearchSource string       `json:"-"`
	Title        string       `json:"title,omitzero"`

	// For tool_use blocks (in assistant messages)
	ID     string       `json:"id,omitzero"`
	Name   string       `json:"name,omitzero"`
	Input  any          `json:"input,omitzero"`
	Caller *BlockCaller `json:"caller,omitzero"`

	// For tool_result blocks (in user messages)
	ToolUseID string `json:"tool_use_id,omitzero"`
	Content   any    `json:"content,omitzero"` // string or []ContentBlockParam
	IsError   bool   `json:"is_error,omitzero"`

	// For thinking blocks (assistant messages, reasoning round-trip)
	Thinking  string `json:"thinking,omitzero"`
	Signature string `json:"signature,omitzero"`

	// For redacted_thinking blocks
	Data string `json:"data,omitzero"`

	// For compaction blocks
	EncryptedContent string `json:"encrypted_content,omitzero"`

	// Cache breakpoints are read as intent: providers cache the prefix by
	// default, and a 1h TTL asks for extended retention.
	CacheControl *CacheControlParam `json:"cache_control,omitzero"`
}

type CacheControlParam struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitzero"`
}

func (b *ContentBlockParam) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	type block ContentBlockParam
	var wire struct {
		block
		Source jsontext.Value `json:"source"`
	}
	if err := json.UnmarshalDecode(dec, &wire, jsonutil.PreserveNumbers); err != nil {
		return err
	}
	value := ContentBlockParam(wire.block)
	if len(wire.Source) > 0 {
		if value.Type == "search_result" {
			if err := json.Unmarshal(wire.Source, &value.SearchSource, dec.Options()); err != nil {
				return err
			}
		} else {
			if err := json.Unmarshal(wire.Source, &value.Source, dec.Options(), jsonutil.PreserveNumbers); err != nil {
				return err
			}
		}
	}
	*b = value
	return nil
}

// BlockSource is the source object for image and document content blocks.
// Image: base64 or url with media_type.
// Document: base64 (PDF), text (plain), url (PDF), or content (string or content blocks).
type BlockSource struct {
	Type      string `json:"type"` // "base64", "url", "text", "content"
	MediaType string `json:"media_type,omitzero"`
	Data      string `json:"data,omitzero"`
	URL       string `json:"url,omitzero"`
	Content   any    `json:"content,omitzero"` // for type=content: string or []ContentBlockParam
}

type ToolParam struct {
	Type        string         `json:"type,omitzero"` // "custom" for regular tools
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Strict      *bool          `json:"strict,omitzero"`

	DeferLoading bool `json:"defer_loading,omitzero"`

	// For computer_* tool types
	DisplayWidthPx  int `json:"display_width_px,omitzero"`
	DisplayHeightPx int `json:"display_height_px,omitzero"`

	// For text_editor_* tool types
	MaxCharacters int `json:"max_characters,omitzero"`

	CacheControl *CacheControlParam `json:"cache_control,omitzero"`
}

type ToolChoice struct {
	Type string `json:"type"` // "auto", "any", "tool", "none"
	Name string `json:"name,omitzero"`

	DisableParallelToolUse bool `json:"disable_parallel_tool_use,omitzero"`
}

// Count tokens types

type CountTokensRequest struct {
	Model    string         `json:"model"`
	Messages []MessageParam `json:"messages"`
	System   any            `json:"system,omitzero"` // string or text blocks
	Tools    []ToolParam    `json:"tools,omitempty"`
}

type CountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// Response types

type Message struct {
	ID    string `json:"id"`
	Type  string `json:"type"` // "message"
	Model string `json:"model"`
	Role  string `json:"role"` // "assistant"

	StopReason   *StopReason  `json:"stop_reason"`
	StopDetails  *StopDetails `json:"stop_details"`
	StopSequence *string      `json:"stop_sequence"`

	Content []ContentBlock `json:"content"`

	// Container is the code execution container of the turn. No backend runs
	// one through the gateway, so it is null, as on turns without one.
	Container *Container `json:"container"`
	// No prompt-cache diagnostics are requested by the gateway.
	Diagnostics jsontext.Value `json:"diagnostics"`

	Usage Usage `json:"usage"`
}

// Container identifies a server-side code execution container.
type Container struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
}

type ContentBlock struct {
	Type string `json:"type"` // "text", "tool_use", "thinking", "redacted_thinking", or "compaction"

	// For text blocks
	Text *string `json:"text,omitzero"`

	// For thinking blocks
	Thinking  string `json:"thinking,omitzero"`
	Signature string `json:"signature,omitzero"`

	// For redacted_thinking blocks
	Data string `json:"data,omitzero"`

	// For compaction blocks
	Content          any    `json:"content,omitzero"`
	EncryptedContent string `json:"encrypted_content,omitzero"`
	ToolUseID        string `json:"tool_use_id,omitzero"`

	// For tool_use blocks
	ID     string       `json:"id,omitzero"`
	Name   string       `json:"name,omitzero"`
	Input  any          `json:"input,omitzero"`
	Caller *BlockCaller `json:"caller,omitzero"`
}

type BlockCaller struct {
	Type string `json:"type"` // "direct"
}

// Thinking blocks must carry an empty thinking string when only the opaque
// continuation signature is visible, including in content_block_start events.
func (b ContentBlock) MarshalJSONTo(enc *jsontext.Encoder) error {
	type block ContentBlock
	var thinking, signature *string
	if b.Signature != "" {
		signature = &b.Signature
	}
	if b.Type == "thinking" {
		thinking, signature = &b.Thinking, &b.Signature
	}
	return json.MarshalEncode(enc, struct {
		block
		Thinking  *string `json:"thinking,omitzero"`
		Signature *string `json:"signature,omitzero"`
	}{block: block(b), Thinking: thinking, Signature: signature})
}

type StopReason string

const (
	StopReasonEndTurn                    StopReason = "end_turn"
	StopReasonMaxTokens                  StopReason = "max_tokens"
	StopReasonStopSequence               StopReason = "stop_sequence"
	StopReasonToolUse                    StopReason = "tool_use"
	StopReasonPauseTurn                  StopReason = "pause_turn"
	StopReasonCompaction                 StopReason = "compaction"
	StopReasonRefusal                    StopReason = "refusal"
	StopReasonModelContextWindowExceeded StopReason = "model_context_window_exceeded"
)

type StopDetails struct {
	Type        string `json:"type"`              // "refusal"
	Category    string `json:"category,omitzero"` // e.g. "cyber"
	Explanation string `json:"explanation,omitzero"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitzero"`

	CacheReadInputTokens     int            `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int            `json:"cache_creation_input_tokens"`
	CacheCreation            *CacheCreation `json:"cache_creation,omitzero"`

	Speed        string `json:"speed,omitzero"`
	ServiceTier  string `json:"service_tier,omitzero"`
	InferenceGeo string `json:"inference_geo,omitzero"`
}

type OutputTokensDetails struct {
	ThinkingTokens int `json:"thinking_tokens"`
}

type CacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

// Streaming event types

type MessageStartEvent struct {
	Type    string  `json:"type"` // "message_start"
	Message Message `json:"message"`
}

type ContentBlockStartEvent struct {
	Type         string       `json:"type"` // "content_block_start"
	Index        int          `json:"index"`
	ContentBlock ContentBlock `json:"content_block"`
}

type ContentBlockDeltaEvent struct {
	Type  string `json:"type"` // "content_block_delta"
	Index int    `json:"index"`
	Delta Delta  `json:"delta"`
}

type Delta struct {
	Type        string `json:"type"` // "text_delta", "input_json_delta", "thinking_delta", "signature_delta", or "compaction_delta"
	Text        string `json:"text,omitzero"`
	PartialJSON string `json:"partial_json,omitzero"`
	Thinking    string `json:"thinking,omitzero"`
	Signature   string `json:"signature,omitzero"`

	// For compaction_delta
	Content          string `json:"content,omitzero"`
	EncryptedContent string `json:"encrypted_content,omitzero"`
}

type ContentBlockStopEvent struct {
	Type  string `json:"type"` // "content_block_stop"
	Index int    `json:"index"`
}

type MessageDeltaEvent struct {
	Type  string       `json:"type"` // "message_delta"
	Delta MessageDelta `json:"delta"`
	Usage DeltaUsage   `json:"usage"`
}

type MessageDelta struct {
	StopReason   StopReason   `json:"stop_reason"`
	StopDetails  *StopDetails `json:"stop_details"`
	StopSequence *string      `json:"stop_sequence"`
	Container    *Container   `json:"container"`
}

type DeltaUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitzero"`

	CacheReadInputTokens     int            `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int            `json:"cache_creation_input_tokens"`
	CacheCreation            *CacheCreation `json:"cache_creation,omitzero"`
}

type MessageStopEvent struct {
	Type string `json:"type"` // "message_stop"
}

type PingEvent struct {
	Type string `json:"type"` // "ping"
}

// Error types

type ErrorResponse struct {
	Type      string `json:"type"` // "error"
	Error     Error  `json:"error"`
	RequestID string `json:"request_id,omitzero"`
}

type Error struct {
	Type    string         `json:"type"` // "invalid_request_error", "authentication_error", etc.
	Message string         `json:"message"`
	Details jsontext.Value `json:"details,omitzero"`
}

// Helper functions for parsing content

func parseContentBlocks(content any) ([]ContentBlockParam, error) {
	if content == nil {
		return nil, nil
	}

	switch v := content.(type) {
	case string:
		return []ContentBlockParam{{Type: "text", Text: v}}, nil
	case []any:
		var blocks []ContentBlockParam
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		if err := decodeRequest(bytes.NewReader(data), &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	default:
		// Try to marshal and unmarshal
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var blocks []ContentBlockParam
		if err := decodeRequest(bytes.NewReader(data), &blocks); err != nil {
			// Try as single block
			var block ContentBlockParam
			if err := decodeRequest(bytes.NewReader(data), &block); err != nil {
				return nil, err
			}
			return []ContentBlockParam{block}, nil
		}
		return blocks, nil
	}
}

func parseSystemContent(system any) (string, error) {
	blocks, err := parseContentBlocks(system)
	if err != nil {
		return "", fmt.Errorf("system: %w", err)
	}
	var result string
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("system: only text blocks are supported")
		}
		if result != "" {
			result += "\n"
		}
		result += block.Text
	}
	return result, nil
}
