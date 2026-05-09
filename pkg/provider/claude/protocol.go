package claude

import "encoding/json"

// Wire-level types for the Claude Code stream-json protocol. Shapes follow the
// reference implementations in claude-agent-sdk-python (_internal/message_parser.py)
// and the reverse-engineered protocol doc.

// envelope is the union shape used to dispatch incoming frames by type/subtype
// before unmarshalling into the concrete frame.
type envelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	SessionID string `json:"session_id,omitempty"`

	// system/init
	Model string `json:"model,omitempty"`

	// assistant
	Message *cliMessage `json:"message,omitempty"`

	// result
	IsError    bool      `json:"is_error,omitempty"`
	Result     string    `json:"result,omitempty"`
	StopReason string    `json:"stop_reason,omitempty"`
	Usage      *cliUsage `json:"usage,omitempty"`
	Errors     []string  `json:"errors,omitempty"`

	// control_request / control_response
	RequestID string           `json:"request_id,omitempty"`
	Request   *json.RawMessage `json:"request,omitempty"`
	Response  *json.RawMessage `json:"response,omitempty"`
}

type cliMessage struct {
	ID    string `json:"id,omitempty"`
	Model string `json:"model,omitempty"`

	Content []cliContent `json:"content,omitempty"`
}

// cliContent covers every assistant/user content block we read or write.
type cliContent struct {
	Type string `json:"type"`

	// text / refusal
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	ResultData   json.RawMessage `json:"content,omitempty"`
	ResultIsErr  *bool           `json:"is_error,omitempty"`

	// image / document
	Source *cliSource `json:"source,omitempty"`
}

type cliSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type cliUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// userFrame is a single NDJSON line written to the CLI's stdin to deliver a
// user turn. Multimodal content goes through Content (a slice of cliContent);
// for plain text the simple {"role":"user","content":"..."} form also works
// but we always emit the array form for consistency.
type userFrame struct {
	Type    string         `json:"type"`
	Message userFrameInner `json:"message"`
}

type userFrameInner struct {
	Role    string       `json:"role"`
	Content []cliContent `json:"content"`
}

// controlRequest envelope written to stdin (e.g. initialize, set_model,
// interrupt) or read from stdout (can_use_tool, mcp_message, hook_callback).
type controlRequestFrame struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// controlResponse envelope, written to stdin to answer a CLI control_request
// or read from stdout to satisfy one of ours.
type controlResponseFrame struct {
	Type     string             `json:"type"`
	Response controlResponseAny `json:"response"`
}

type controlResponseAny struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Initialize request payload — we only send the subtype handshake; hooks,
// agents, skills, etc. are not exposed yet.
type initializeRequest struct {
	Subtype string `json:"subtype"`
}

// can_use_tool request, as received from the CLI.
type canUseToolRequest struct {
	Subtype     string         `json:"subtype"`
	ToolName    string         `json:"tool_name"`
	Input       map[string]any `json:"input"`
	ToolUseID   string         `json:"tool_use_id,omitempty"`
	BlockedPath string         `json:"blocked_path,omitempty"`

	DecisionReason string `json:"decision_reason,omitempty"`
	Title          string `json:"title,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	Description    string `json:"description,omitempty"`
}

// mcp_message request, as received from the CLI.
type mcpMessageRequest struct {
	Subtype    string          `json:"subtype"`
	ServerName string          `json:"server_name"`
	Message    json.RawMessage `json:"message"`
}

// JSON-RPC types used inside an mcp_message envelope.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

