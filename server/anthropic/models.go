package anthropic

import (
	"encoding/json"
)

// Request types

type MessageRequest struct {
	Model         string         `json:"model"`
	Messages      []MessageParam `json:"messages"`
	System        any            `json:"system,omitempty"` // string or []SystemBlock
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	Temperature   *float32       `json:"temperature,omitempty"`
	TopP          *float32       `json:"top_p,omitempty"`
	TopK          *int           `json:"top_k,omitempty"`
	StopSequences []string       `json:"stop_sequences,omitempty"`
	Tools         []ToolParam    `json:"tools,omitempty"`
	ToolChoice    *ToolChoice    `json:"tool_choice,omitempty"`
	Metadata      *Metadata      `json:"metadata,omitempty"`
}

type MessageParam struct {
	Role    MessageRole `json:"role"`
	Content any         `json:"content"` // string or []ContentBlockParam
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

type ContentBlockParam struct {
	Type string `json:"type"`

	// For text blocks
	Text string `json:"text,omitempty"`

	// For image blocks
	Source *ImageSource `json:"source,omitempty"`

	// For tool_use blocks (in assistant messages)
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`

	// For tool_result blocks (in user messages)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"` // string or []ContentBlockParam
	IsError   bool   `json:"is_error,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"` // "base64" or "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type SystemBlock struct {
	Type         string        `json:"type"` // "text"
	Text         string        `json:"text,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type ToolParam struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type ToolChoice struct {
	Type string `json:"type"` // "auto", "any", "tool"
	Name string `json:"name,omitempty"`
}

type Metadata struct {
	UserID string `json:"user_id,omitempty"`
}

// Response types

type Message struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // "message"
	Role         string         `json:"role"` // "assistant"
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   StopReason     `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

type ContentBlock struct {
	Type string `json:"type"` // "text" or "tool_use"

	// For text blocks
	Text string `json:"text,omitempty"`

	// For tool_use blocks
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
	StopReasonToolUse      StopReason = "tool_use"
)

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Streaming event types

type StreamEvent struct {
	Type string `json:"type"`
}

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
	Type        string `json:"type"` // "text_delta" or "input_json_delta"
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
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
	StopReason   StopReason `json:"stop_reason"`
	StopSequence *string    `json:"stop_sequence"`
}

type DeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type MessageStopEvent struct {
	Type string `json:"type"` // "message_stop"
}

type PingEvent struct {
	Type string `json:"type"` // "ping"
}

// Error types

type ErrorResponse struct {
	Type  string `json:"type"` // "error"
	Error Error  `json:"error"`
}

type Error struct {
	Type    string `json:"type"` // "invalid_request_error", "authentication_error", etc.
	Message string `json:"message"`
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
		if err := json.Unmarshal(data, &blocks); err != nil {
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
		if err := json.Unmarshal(data, &blocks); err != nil {
			// Try as single block
			var block ContentBlockParam
			if err := json.Unmarshal(data, &block); err != nil {
				return nil, err
			}
			return []ContentBlockParam{block}, nil
		}
		return blocks, nil
	}
}

func parseSystemContent(system any) (string, error) {
	if system == nil {
		return "", nil
	}

	switch v := system.(type) {
	case string:
		return v, nil
	case []any:
		// Array of system blocks
		var result string
		for _, item := range v {
			data, err := json.Marshal(item)
			if err != nil {
				return "", err
			}
			var block SystemBlock
			if err := json.Unmarshal(data, &block); err != nil {
				return "", err
			}
			if block.Type == "text" {
				if result != "" {
					result += "\n"
				}
				result += block.Text
			}
		}
		return result, nil
	default:
		return "", nil
	}
}
