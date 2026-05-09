package claude

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"os"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config

	sessions *sessionStore
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		model:   model,
		command: "claude",
	}

	for _, option := range options {
		option(cfg)
	}

	if cfg.command == "" {
		cfg.command = "claude"
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

		system, frame, ok := convertMessages(messages)
		if !ok {
			yield(nil, errors.New("claude: messages must be non-empty and end with a user turn"))
			return
		}

		// Resolve a cached session_id for the conversation prefix (everything
		// before the new user turn).
		prefix := messages[:len(messages)-1]
		resumeID, _ := c.sessions.get(keyFor(prefix))

		if len(prefix) > 0 && resumeID == "" {
			slog.Warn("claude: cold-cache multi-turn conversation; prior turns will be lost",
				"turns", len(prefix))
		}

		// Advertise caller-supplied tools through our in-process MCP server.
		mcp := newMcpServer(options.Tools)

		mcpConfig, err := writeMcpConfig()
		if err != nil {
			yield(nil, err)
			return
		}
		defer os.Remove(mcpConfig)

		args := c.buildArgs(options, system, resumeID, mcpConfig)

		t, err := startTransport(ctx, c.command, args, processEnv(), "")
		if err != nil {
			yield(nil, err)
			return
		}
		defer t.close()

		q := newQuery(t, mcp)
		defer q.stop()

		initCtx, initCancel := context.WithTimeout(ctx, 60*time.Second)
		err = q.initialize(initCtx, 60*time.Second)
		initCancel()
		if err != nil {
			yield(nil, convertError(t.stderrText(), "", err))
			return
		}

		if err := t.send(frame); err != nil {
			yield(nil, err)
			return
		}

		c.runLoop(ctx, t, q, messages, yield)
	}
}

// runLoop drains the transport, dispatches control requests, and yields
// completions until a `result` frame arrives, ctx is cancelled, or stdout
// closes. All error reporting goes through `yield`; the caller does not need
// to surface anything afterwards.
func (c *Completer) runLoop(
	ctx context.Context,
	t *transport,
	q *query,
	messages []provider.Message,
	yield func(*provider.Completion, error) bool,
) {
	var (
		messageID string
		modelName = c.model
		sessionID string
	)

	for {
		select {
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return

		case env, alive := <-q.messages:
			if !alive {
				// Stdout closed before we saw a `result` — the CLI exited
				// early. Drain it so stderr is fully captured, then surface.
				t.close()
				yield(nil, convertError(t.stderrText(), "", errors.New("claude: cli exited without a result")))
				return
			}

			switch env.Type {
			case "system":
				if env.Subtype == "init" {
					if env.SessionID != "" {
						sessionID = env.SessionID
					}
					if env.Model != "" && (modelName == "" || modelName == c.model) {
						modelName = env.Model
					}
				}

			case "assistant":
				if env.Message == nil {
					continue
				}
				if env.Message.ID != "" {
					messageID = env.Message.ID
				}
				if env.Message.Model != "" && env.Message.Model != "<synthetic>" {
					modelName = env.Message.Model
				}
				if env.SessionID != "" {
					sessionID = env.SessionID
				}

				for _, block := range env.Message.Content {
					content, ok := convertCliContent(block)
					if !ok {
						continue
					}
					if !yieldContent(yield, messageID, modelName, content) {
						return
					}
				}

			case "result":
				if env.SessionID != "" {
					sessionID = env.SessionID
				}
				if env.IsError {
					msg := env.Result
					if msg == "" && len(env.Errors) > 0 {
						msg = env.Errors[0]
					}
					yield(nil, convertError(t.stderrText(), msg, errors.New("claude: error result")))
					return
				}

				final := &provider.Completion{
					ID:     messageID,
					Model:  modelName,
					Status: convertStopReason(env.StopReason),
					Usage:  convertUsage(env.Usage),
					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,
					},
				}
				if !yield(final, nil) {
					return
				}

				// Cache the (possibly forked) session_id under the user-
				// content hash of this turn's full message list. keyFor
				// ignores assistant content so caller-side round-trip
				// fidelity doesn't matter for cache hits next turn.
				if sessionID != "" {
					c.sessions.put(keyFor(messages), sessionID)
				}
				return
			}
		}
	}
}

func (c *Completer) buildArgs(opts *provider.CompleteOptions, system, resumeID, mcpConfig string) []string {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	}

	if c.model != "" {
		args = append(args, "--model", c.model)
	}

	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}

	args = append(args, thinkingFlags(opts.ReasoningOptions)...)

	// Lock the model to caller-supplied tools only — the CLI's built-in tools
	// (Bash, Read, Write, Edit, …) would otherwise be reachable and cause
	// "Tool 'X' is not available or not executable" when the model picks a
	// built-in not on the allowed list. `--tools ""` gives the model an empty
	// base toolset; `--allowedTools mcp__wingman__<name>` selectively adds
	// back only the tools the caller passed.
	args = append(args, "--tools", "")
	for _, t := range opts.Tools {
		args = append(args, "--allowedTools", toolPrefix()+t.Name)
	}

	if mcpConfig != "" {
		args = append(args, "--mcp-config", mcpConfig)
	}
	if resumeID != "" {
		args = append(args, "--resume", resumeID, "--fork-session")
	}

	return args
}

// thinkingFlags maps caller-supplied ReasoningOptions to the CLI's --thinking
// / --max-thinking-tokens flags. Effort levels follow the Anthropic API
// ranges roughly.
func thinkingFlags(opts *provider.ReasoningOptions) []string {
	if opts == nil {
		return nil
	}

	switch opts.Effort {
	case provider.EffortNone:
		return []string{"--thinking", "disabled"}
	case provider.EffortMinimal:
		return []string{"--thinking", "adaptive"}
	case provider.EffortLow:
		return []string{"--max-thinking-tokens", "4000"}
	case provider.EffortMedium:
		return []string{"--max-thinking-tokens", "8000"}
	case provider.EffortHigh:
		return []string{"--max-thinking-tokens", "16000"}
	case provider.EffortXHigh, provider.EffortMax:
		return []string{"--max-thinking-tokens", "32000"}
	}
	return nil
}

// processEnv builds a fresh, minimal environment for the spawned CLI rather
// than inheriting the full host env. We pass through only what the CLI
// actually needs to run and authenticate; everything else (including
// ANTHROPIC_API_KEY, which would force API-key auth and reject OAuth) is
// dropped. CLAUDE_CODE_ENTRYPOINT marks us as an SDK caller.
func processEnv() []string {
	allowed := []string{
		// Filesystem essentials: $HOME (CLI session store, OAuth creds),
		// USER, LOGNAME, SHELL.
		"HOME", "USER", "LOGNAME", "SHELL",
		// Process discovery + temp paths.
		"PATH", "TMPDIR", "TEMP", "TMP",
		// Locale (so the CLI picks the right text encoding).
		"LANG", "LC_ALL", "LC_CTYPE",
		// Terminal info — the CLI may probe these even in --print mode.
		"TERM",
		// Proxy support if the host configured one.
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
		// Claude-specific overrides the user may have set.
		"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
		"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT",
	}

	env := make([]string, 0, len(allowed)+1)
	for _, key := range allowed {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	env = append(env, "CLAUDE_CODE_ENTRYPOINT=sdk-go")
	return env
}

// writeMcpConfig writes a temporary mcp-config JSON registering our SDK MCP
// server so the CLI knows to route caller-defined tools back to us.
func writeMcpConfig() (string, error) {
	payload, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			mcpServerName: map[string]any{"type": "sdk", "name": mcpServerName},
		},
	})
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp("", "wingman-claude-mcp-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := f.Write(payload); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
