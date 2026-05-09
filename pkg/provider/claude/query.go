package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// query layers the bidirectional control protocol on top of the transport.
// It owns:
//   - request_id correlation for control_requests we send (set_model,
//     interrupt, mcp_status, …)
//   - dispatch of control_requests received from the CLI (can_use_tool,
//     mcp_message, hook_callback)
//   - the in-process MCP server
//
// Mirrors claude_agent_sdk._internal.query.Query.
type query struct {
	t   *transport
	mcp *mcpServer

	mu      sync.Mutex
	pending map[string]chan controlResponseAny

	counter uint64

	// messages carries non-control envelopes (system, assistant, result) to
	// whoever is running the streaming loop. control_request and
	// control_response frames are handled internally by the dispatcher.
	messages chan envelope

	dispatchCtx    context.Context
	dispatchCancel context.CancelFunc
	dispatchDone   chan struct{}
}

func newQuery(t *transport, mcp *mcpServer) *query {
	q := &query{
		t:            t,
		mcp:          mcp,
		pending:      make(map[string]chan controlResponseAny),
		messages:     make(chan envelope, 64),
		dispatchDone: make(chan struct{}),
	}
	q.dispatchCtx, q.dispatchCancel = context.WithCancel(context.Background())
	go q.dispatch()
	return q
}

// dispatch demultiplexes the transport's output channel: control frames stay
// here (response routing or per-request goroutine), everything else is
// forwarded to the message channel for the main loop to consume.
func (q *query) dispatch() {
	defer close(q.dispatchDone)
	defer close(q.messages)

	for {
		select {
		case env, ok := <-q.t.out:
			if !ok {
				return
			}
			switch env.Type {
			case "control_response":
				q.routeControlResponse(env)
			case "control_request":
				go q.handleControlRequest(q.dispatchCtx, env)
			default:
				select {
				case q.messages <- env:
				case <-q.dispatchCtx.Done():
					return
				}
			}
		case <-q.dispatchCtx.Done():
			return
		}
	}
}

// stop terminates the dispatcher (called by the completer on teardown).
func (q *query) stop() {
	q.dispatchCancel()
	<-q.dispatchDone
}

// initialize sends the initialize control_request. Only hooks/agents/skills
// would be carried here; we don't expose those yet, so this is essentially a
// handshake that gives the CLI a chance to register our MCP server.
func (q *query) initialize(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	req := initializeRequest{Subtype: "initialize"}

	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}

	_, err = q.sendControlRequest(ctx, payload, timeout)
	return err
}

// sendControlRequest writes a control_request envelope and waits for the
// matching control_response (or ctx/timeout). The caller passes the inner
// request payload as a pre-marshalled JSON blob.
func (q *query) sendControlRequest(ctx context.Context, request json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	q.mu.Lock()
	q.counter++
	id := fmt.Sprintf("req_%d_%s", q.counter, uuid.NewString()[:8])
	ch := make(chan controlResponseAny, 1)
	q.pending[id] = ch
	q.mu.Unlock()

	defer func() {
		q.mu.Lock()
		delete(q.pending, id)
		q.mu.Unlock()
	}()

	frame := controlRequestFrame{
		Type:      "control_request",
		RequestID: id,
		Request:   request,
	}

	if err := q.t.send(frame); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-ch:
		if resp.Subtype == "error" {
			return nil, fmt.Errorf("claude: control request failed: %s", resp.Error)
		}
		return resp.Response, nil
	case <-timer.C:
		return nil, fmt.Errorf("claude: control request %s timed out", id)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// routeControlResponse delivers an incoming control_response to whichever
// sendControlRequest is waiting on it. Unknown ids are dropped.
func (q *query) routeControlResponse(env envelope) {
	if env.Response == nil {
		return
	}

	var resp controlResponseAny
	if err := json.Unmarshal(*env.Response, &resp); err != nil {
		return
	}

	q.mu.Lock()
	ch, ok := q.pending[resp.RequestID]
	q.mu.Unlock()

	if !ok {
		return
	}

	select {
	case ch <- resp:
	default:
	}
}

// handleControlRequest dispatches a control_request received from the CLI.
// Runs on a fresh goroutine so a long-running tool call doesn't block the
// reader.
func (q *query) handleControlRequest(ctx context.Context, env envelope) {
	if env.Request == nil {
		return
	}

	var head struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(*env.Request, &head); err != nil {
		q.replyError(env.RequestID, "invalid request: "+err.Error())
		return
	}

	switch head.Subtype {
	case "can_use_tool":
		q.handleCanUseTool(ctx, env.RequestID, *env.Request)
	case "mcp_message":
		q.handleMcpMessage(ctx, env.RequestID, *env.Request)
	case "hook_callback":
		// We don't expose hooks yet; ack so the CLI doesn't stall.
		q.replySuccess(env.RequestID, map[string]any{"continue": true})
	default:
		q.replyError(env.RequestID, "unsupported control request subtype: "+head.Subtype)
	}
}

func (q *query) handleCanUseTool(ctx context.Context, requestID string, raw json.RawMessage) {
	var req canUseToolRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		q.replyError(requestID, "invalid can_use_tool: "+err.Error())
		return
	}

	// Auto-allow every tool. The host doesn't gate built-in CLI tools at
	// this layer; if you need to restrict them, configure the CLI's
	// `--permission-mode` / `--allowedTools` directly.
	q.replySuccess(requestID, map[string]any{
		"behavior":     "allow",
		"updatedInput": req.Input,
	})
}

func (q *query) handleMcpMessage(ctx context.Context, requestID string, raw json.RawMessage) {
	if q.mcp == nil {
		q.replyError(requestID, "no in-process MCP server registered")
		return
	}

	var req mcpMessageRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		q.replyError(requestID, "invalid mcp_message: "+err.Error())
		return
	}

	if req.ServerName != mcpServerName {
		q.replyError(requestID, "unknown MCP server: "+req.ServerName)
		return
	}

	resp := q.mcp.dispatch(ctx, req.Message)
	q.replySuccess(requestID, map[string]any{"mcp_response": resp})
}

func (q *query) replySuccess(requestID string, response any) {
	raw, err := json.Marshal(response)
	if err != nil {
		q.replyError(requestID, "internal: "+err.Error())
		return
	}

	frame := controlResponseFrame{
		Type: "control_response",
		Response: controlResponseAny{
			Subtype:   "success",
			RequestID: requestID,
			Response:  raw,
		},
	}
	_ = q.t.send(frame)
}

func (q *query) replyError(requestID, message string) {
	frame := controlResponseFrame{
		Type: "control_response",
		Response: controlResponseAny{
			Subtype:   "error",
			RequestID: requestID,
			Error:     message,
		},
	}
	_ = q.t.send(frame)
}

