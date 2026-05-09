package codex

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Completer = (*Completer)(nil)

const (
	clientName    = "wingman"
	clientVersion = "1.0.0"

	// "Yolo" defaults: no approvals, full sandbox access. We use codex as a
	// chat-completion backend, so prompting the host operator for every shell
	// command would deadlock the iterator.
	yoloApprovalPolicy = "never"
	yoloSandbox        = "danger-full-access"

	// Codex emits a `dynamic_tool_call` server request that we have to answer
	// inline within the turn. Wingman's iterator surface can't ferry the
	// caller's tool result back into the still-open turn, so we answer with a
	// deferral marker and the model recovers via the next Complete() call
	// (which carries the tool result as a follow-up user message).
	deferredToolMarker = "[wingman: tool result will be supplied by the host on the next turn]"
)

type Completer struct {
	*Config

	sessions *sessionStore
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		model:   model,
		command: "codex",
	}

	for _, option := range options {
		option(cfg)
	}

	if cfg.command == "" {
		cfg.command = "codex"
	}

	return &Completer{
		Config:   cfg,
		sessions: newSessionStore(256, 30*time.Minute),
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		system, inputs, tempImages, ok := convertMessages(messages)
		defer cleanupPaths(tempImages)
		if !ok {
			yield(nil, errors.New("codex: messages must be non-empty and end with a user turn"))
			return
		}

		// Look up a cached thread_id for the conversation prefix.
		prefix := messages[:len(messages)-1]
		resumeID, _ := c.sessions.get(keyFor(prefix))

		if len(prefix) > 0 && resumeID == "" {
			slog.Warn("codex: cold-cache multi-turn conversation; prior turns will be lost",
				"turns", len(prefix))
		}

		t, err := startTransport(ctx, c.command, appServerArgs(), processEnv(), "")
		if err != nil {
			yield(nil, err)
			return
		}
		defer t.close()

		q := newQuery(t)
		defer q.stop()

		// 1. initialize handshake
		initCtx, initCancel := context.WithTimeout(ctx, 60*time.Second)
		_, err = q.call(initCtx, "initialize", initializeParams{
			ClientInfo: clientInfo{
				Name:    clientName,
				Version: clientVersion,
			},
			Capabilities: &initializeCapabilities{ExperimentalAPI: true},
		}, 60*time.Second)
		initCancel()
		if err != nil {
			yield(nil, convertError(t.stderrText(), "", err))
			return
		}
		if err := q.notify("initialized", nil); err != nil {
			yield(nil, err)
			return
		}


		// 3. thread/start (or thread/resume)
		threadID, err := c.openThread(ctx, q, system, resumeID, options)
		if err != nil {
			yield(nil, convertError(t.stderrText(), "", err))
			return
		}

		// 4. turn/start
		turnCtx, turnCancel := context.WithTimeout(ctx, 60*time.Second)
		turnResp, err := q.call(turnCtx, "turn/start", turnStartParams{
			ThreadID:     threadID,
			Input:        inputs,
			Model:        c.model,
			Effort:       reasoningEffort(options),
			OutputSchema: schemaToMap(options.Schema),
		}, 60*time.Second)
		turnCancel()
		if err != nil {
			yield(nil, convertError(t.stderrText(), "", err))
			return
		}

		var turnStart turnStartResponse
		if err := json.Unmarshal(turnResp, &turnStart); err != nil {
			yield(nil, err)
			return
		}

		// 5. drain notifications until turn/completed (or error)
		c.runLoop(ctx, t, q, threadID, turnStart.Turn.ID, messages, yield)
	}
}

// openThread starts a fresh thread or resumes an existing one. Returns the
// active thread_id (which may be a new id from a fresh start or the resumed
// id).
func (c *Completer) openThread(ctx context.Context, q *query, system, resumeID string, options *provider.CompleteOptions) (string, error) {
	if resumeID != "" {
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		raw, err := q.call(callCtx, "thread/resume", threadResumeParams{
			ThreadID: resumeID,
			Model:    c.model,
		}, 60*time.Second)
		if err == nil {
			var resp threadResumeResponse
			if jerr := json.Unmarshal(raw, &resp); jerr == nil && resp.Thread.ID != "" {
				return resp.Thread.ID, nil
			}
		}
		// Fall through to a fresh thread/start if resume fails (the cached
		// thread may have been GC'd by codex).
	}

	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	params := threadStartParams{
		Model:            c.model,
		BaseInstructions: system,
		Sandbox:          yoloSandbox,
		ApprovalPolicy:   yoloApprovalPolicy,
	}
	if tools := dynamicToolsFor(options); len(tools) > 0 {
		params.DynamicTools = tools
	}

	raw, err := q.call(startCtx, "thread/start", params, 60*time.Second)
	if err != nil {
		return "", err
	}

	var resp threadStartResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.Thread.ID, nil
}

// runLoop pumps notifications and inbound server-requests until turn/completed
// or an error. Yields provider.Completion deltas as they arrive and a final
// terminal Completion when the turn ends.
func (c *Completer) runLoop(
	ctx context.Context,
	t *transport,
	q *query,
	threadID, turnID string,
	messages []provider.Message,
	yield func(*provider.Completion, error) bool,
) {
	var (
		modelName  = c.model
		messageID  string
		usage      *tokenUsageBreakdown
		seenStatus provider.CompletionStatus
		seenError  string
	)

	for {
		select {
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return

		case msg, alive := <-q.notifications:
			if !alive {
				t.close()
				yield(nil, convertError(t.stderrText(), "", errors.New("codex: server exited unexpectedly")))
				return
			}

			switch msg.Method {
			case "thread/started":
				// already captured at thread/start; nothing to do

			case "turn/started":
				// nothing actionable here

			case "item/started":
				var n itemNotification
				_ = json.Unmarshal(msg.Params, &n)
				if id := emitToolCallIfDynamic(yield, modelName, n.Item); id != "" {
					messageID = id
				}

			case "item/completed":
				var n itemNotification
				_ = json.Unmarshal(msg.Params, &n)
				id := handleCompletedItem(yield, modelName, n.Item)
				if id != "" {
					messageID = id
				}

			case "item/agentMessage/delta":
				var d agentMessageDelta
				_ = json.Unmarshal(msg.Params, &d)
				if d.ItemID != "" {
					messageID = d.ItemID
				}
				if !yieldText(yield, messageID, modelName, d.Delta) {
					return
				}

			case "item/reasoning/textDelta":
				var d reasoningTextDelta
				_ = json.Unmarshal(msg.Params, &d)
				if d.ItemID != "" {
					messageID = d.ItemID
				}
				if !yieldReasoning(yield, messageID, modelName, d.Delta) {
					return
				}

			case "item/reasoning/summaryTextDelta":
				var d reasoningSummaryDelta
				_ = json.Unmarshal(msg.Params, &d)
				if d.ItemID != "" {
					messageID = d.ItemID
				}
				if !yieldReasoning(yield, messageID, modelName, d.Delta) {
					return
				}

			case "thread/tokenUsage/updated":
				var u tokenUsageNotification
				if err := json.Unmarshal(msg.Params, &u); err == nil {
					last := u.TokenUsage.Last
					usage = &last
				}

			case "turn/completed":
				var n turnCompletedNotification
				_ = json.Unmarshal(msg.Params, &n)
				seenStatus = convertTurnStatus(n.Turn.Status)
				if n.Turn.Error != nil {
					seenError = n.Turn.Error.Message
					seenStatus = provider.CompletionStatusFailed
				}

				final := &provider.Completion{
					ID:     messageID,
					Model:  modelName,
					Status: seenStatus,
					Usage:  convertUsage(usage),
					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,
					},
				}

				if seenStatus == provider.CompletionStatusFailed && seenError != "" {
					yield(nil, convertError(t.stderrText(), seenError, errors.New("codex: turn failed")))
					return
				}

				if !yield(final, nil) {
					return
				}

				// Cache the thread_id under the user-content hash of this
				// turn's full message list so the next turn can resume.
				if threadID != "" {
					c.sessions.put(keyFor(messages), threadID)
				}
				return

			case "error":
				var e errorNotification
				_ = json.Unmarshal(msg.Params, &e)
				yield(nil, convertError(t.stderrText(), e.Message, errors.New("codex: server error")))
				return
			}

		case req, alive := <-q.requests:
			if !alive {
				continue
			}
			c.handleServerRequest(q, req)
		}
	}
}

// handleServerRequest answers JSON-RPC requests sent from codex to us.
// Currently we know about item/tool/call (DynamicToolCall); everything else
// is rejected with a method-not-found error so codex can surface it.
//
// We do NOT emit a ToolCall delta here — the accompanying `item/started`
// notification already did. We only need to answer codex with the deferral
// placeholder so the turn can wind down without blocking.
func (c *Completer) handleServerRequest(
	q *query,
	req jsonrpcMessage,
) {
	switch req.Method {
	case "item/tool/call":
		_ = q.reply(req.ID, dynamicToolCallResponse{
			ContentItems: []dynamicToolContentItem{{Type: "inputText", Text: deferredToolMarker}},
			Success:      false,
		})

	default:
		_ = q.replyError(req.ID, -32601, "method not implemented: "+req.Method)
	}
}

// emitToolCallIfDynamic emits a tool-call delta for an in-progress
// `dynamicToolCall` item the moment it appears, so callers see it before the
// server's separate `item/tool/call` request arrives. Returns the item id so
// the caller can stamp later deltas with the same message id.
func emitToolCallIfDynamic(yield func(*provider.Completion, error) bool, model string, raw json.RawMessage) string {
	var head threadItemHead
	if err := json.Unmarshal(raw, &head); err != nil {
		return ""
	}
	if head.Type != "dynamicToolCall" {
		return head.ID
	}
	var item dynamicToolCallItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return head.ID
	}
	args := string(item.Arguments)
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	yieldToolCall(yield, item.ID, model, provider.ToolCall{
		ID:        item.ID,
		Name:      item.Tool,
		Arguments: args,
	})
	return item.ID
}

// handleCompletedItem deals with non-streaming item types (agent messages
// when the model produces them as a single chunk, reasoning items with no
// preceding deltas, etc.). For dynamic tool calls we already emitted at
// item/started, so completion is a no-op.
func handleCompletedItem(yield func(*provider.Completion, error) bool, model string, raw json.RawMessage) string {
	var head threadItemHead
	if err := json.Unmarshal(raw, &head); err != nil {
		return ""
	}
	switch head.Type {
	case "agentMessage":
		var item agentMessageItem
		if err := json.Unmarshal(raw, &item); err == nil && item.Text != "" {
			// Already streamed via deltas in normal flow; only emit if no
			// deltas were sent (defensive — protects against batched output).
			// We can't easily distinguish here, so the item-completed text is
			// dropped to avoid duplication.
			_ = item
		}
		return head.ID

	case "reasoning":
		var item reasoningItem
		if err := json.Unmarshal(raw, &item); err == nil {
			text := strings.Join(append(item.Summary, item.Content...), "\n")
			if text != "" {
				yieldReasoning(yield, item.ID, model, text)
			}
		}
		return head.ID
	}
	return head.ID
}

func dynamicToolsFor(opts *provider.CompleteOptions) []dynamicToolSpec {
	if opts == nil || len(opts.Tools) == 0 {
		return nil
	}
	out := make([]dynamicToolSpec, 0, len(opts.Tools))
	for _, t := range opts.Tools {
		schema := t.Parameters
		if schema == nil {
			schema = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			}
		}
		out = append(out, dynamicToolSpec{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out
}

func reasoningEffort(opts *provider.CompleteOptions) string {
	if opts == nil || opts.ReasoningOptions == nil {
		return ""
	}
	return effortString(opts.ReasoningOptions.Effort)
}

func schemaToMap(s *provider.Schema) map[string]any {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema
}

func cleanupPaths(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// appServerArgs spawns codex with every side-effecting built-in tool turned
// off. Tools that just clutter the prompt (tool_search, tool_suggest, etc.)
// are left alone — they don't reach the host and don't justify the drift
// risk of a long denylist. If codex adds a new tool that actually does I/O,
// the smoke test will catch it.
func appServerArgs() []string {
	args := []string{"app-server"}
	for _, key := range disabledFeatures {
		args = append(args, "-c", "features."+key+"=false")
	}
	return args
}

var disabledFeatures = []string{
	// shell exec
	"shell_tool",
	"unified_exec",
	// file editing
	"apply_patch_freeform",
	// outbound HTTP
	"web_search_request",
	"web_search_cached",
	// sub-agent spawning
	"multi_agent",
	"multi_agent_v2",
	// browser / GUI
	"browser_use",
	"browser_use_external",
	"in_app_browser",
	"computer_use",
	// image generation
	"image_generation",
}

// processEnv builds a fresh, minimal environment for the spawned codex CLI.
// Codex auth comes from ~/.codex (CODEX_HOME) or env vars (CODEX_API_KEY,
// OPENAI_API_KEY); the binary picks whichever is configured.
func processEnv() []string {
	allowed := []string{
		"HOME", "USER", "LOGNAME", "SHELL",
		"PATH", "TMPDIR", "TEMP", "TMP",
		"LANG", "LC_ALL", "LC_CTYPE",
		"TERM",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
		"CODEX_HOME", "CODEX_API_KEY", "OPENAI_API_KEY",
		"OPENAI_BASE_URL",
	}

	env := make([]string, 0, len(allowed))
	for _, key := range allowed {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}
