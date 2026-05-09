package codex

import "encoding/json"

// JSON-RPC 2.0 envelope used by `codex app-server` (newline-delimited).
//
// A single line is one of:
//   - request:      {jsonrpc, id, method, params}
//   - response:     {jsonrpc, id, result|error}
//   - notification: {jsonrpc, method, params}     (no id)
//
// We don't strictly require `jsonrpc:"2.0"` on inbound — codex always sets it
// — but we always emit it ourselves.

type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *jsonrpcError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ---------------------------------------------------------------------------
// initialize
// ---------------------------------------------------------------------------

type initializeParams struct {
	ClientInfo   clientInfo             `json:"clientInfo"`
	Capabilities *initializeCapabilities `json:"capabilities,omitempty"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type initializeCapabilities struct {
	ExperimentalAPI bool `json:"experimentalApi,omitempty"`
}

// ---------------------------------------------------------------------------
// thread/start
// ---------------------------------------------------------------------------

type threadStartParams struct {
	Model               string             `json:"model,omitempty"`
	ModelProvider       string             `json:"modelProvider,omitempty"`
	Cwd                 string             `json:"cwd,omitempty"`
	BaseInstructions    string             `json:"baseInstructions,omitempty"`
	DeveloperInstructions string           `json:"developerInstructions,omitempty"`
	Sandbox             string             `json:"sandbox,omitempty"`
	ApprovalPolicy      string             `json:"approvalPolicy,omitempty"`
	DynamicTools        []dynamicToolSpec  `json:"dynamicTools,omitempty"`
}

type dynamicToolSpec struct {
	Namespace   string         `json:"namespace,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	DeferLoading bool          `json:"deferLoading,omitempty"`
}

type threadStartResponse struct {
	Thread        threadInfo `json:"thread"`
	Model         string     `json:"model,omitempty"`
	ModelProvider string     `json:"modelProvider,omitempty"`
}

type threadInfo struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId,omitempty"`
	Status    json.RawMessage `json:"status,omitempty"`
}

// ---------------------------------------------------------------------------
// thread/resume
// ---------------------------------------------------------------------------

type threadResumeParams struct {
	ThreadID string `json:"threadId"`
	Model    string `json:"model,omitempty"`
}

type threadResumeResponse struct {
	Thread threadInfo `json:"thread"`
}

// ---------------------------------------------------------------------------
// turn/start  +  turn/interrupt
// ---------------------------------------------------------------------------

type turnStartParams struct {
	ThreadID     string         `json:"threadId"`
	Input        []userInput    `json:"input"`
	Cwd          string         `json:"cwd,omitempty"`
	Model        string         `json:"model,omitempty"`
	Effort       string         `json:"effort,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

// userInput is the tagged enum codex expects on the wire. We only emit the
// variants we care about: text and (local|remote) image. Other variants
// (skill, mention) are codex-UI-specific and have no provider equivalent.
type userInput struct {
	Type string `json:"type"`

	// Text
	Text string `json:"text,omitempty"`

	// Image (URL)
	URL string `json:"url,omitempty"`

	// LocalImage
	Path string `json:"path,omitempty"`
}

type turnStartResponse struct {
	Turn turnInfo `json:"turn"`
}

type turnInfo struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// ---------------------------------------------------------------------------
// notifications: turn lifecycle, items, deltas, usage, errors
// ---------------------------------------------------------------------------

type threadStartedNotification struct {
	Thread threadInfo `json:"thread"`
}

type turnStartedNotification struct {
	ThreadID string   `json:"threadId"`
	Turn     turnInfo `json:"turn"`
}

type turnCompletedNotification struct {
	ThreadID string         `json:"threadId"`
	Turn     turnFullInfo   `json:"turn"`
}

type turnFullInfo struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Error  *turnErr  `json:"error,omitempty"`
}

type turnErr struct {
	Message string `json:"message"`
}

type itemNotification struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	Item     json.RawMessage `json:"item"`
}

// threadItemHead is enough to discriminate the item.type tag without parsing
// the whole payload.
type threadItemHead struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type agentMessageItem struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Phase string `json:"phase,omitempty"`
}

type reasoningItem struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Summary []string `json:"summary,omitempty"`
	Content []string `json:"content,omitempty"`
}

type dynamicToolCallItem struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Namespace    string          `json:"namespace,omitempty"`
	Tool         string          `json:"tool"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	Status       string          `json:"status,omitempty"`
}

type errorNotification struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
}

// per-item delta notifications

type agentMessageDelta struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type reasoningTextDelta struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	ContentIndex int64  `json:"contentIndex"`
}

type reasoningSummaryDelta struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	SummaryIndex int64  `json:"summaryIndex"`
}

type tokenUsageNotification struct {
	ThreadID   string         `json:"threadId"`
	TurnID     string         `json:"turnId"`
	TokenUsage threadUsageAll `json:"tokenUsage"`
}

type threadUsageAll struct {
	Total tokenUsageBreakdown `json:"total"`
	Last  tokenUsageBreakdown `json:"last"`
}

type tokenUsageBreakdown struct {
	TotalTokens            int64 `json:"totalTokens"`
	InputTokens            int64 `json:"inputTokens"`
	CachedInputTokens      int64 `json:"cachedInputTokens"`
	OutputTokens           int64 `json:"outputTokens"`
	ReasoningOutputTokens  int64 `json:"reasoningOutputTokens"`
}

// ---------------------------------------------------------------------------
// server -> client requests we have to answer
// ---------------------------------------------------------------------------

type dynamicToolCallParams struct {
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	CallID    string          `json:"callId"`
	Namespace string          `json:"namespace,omitempty"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type dynamicToolCallResponse struct {
	ContentItems []dynamicToolContentItem `json:"contentItems"`
	Success      bool                     `json:"success"`
}

type dynamicToolContentItem struct {
	Type     string `json:"type"`           // "inputText" | "inputImage"
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
}

