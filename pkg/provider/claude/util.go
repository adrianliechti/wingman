package claude

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// convertError extracts a clean error from CLI output. It looks for a JSON
// envelope {"error":{"type":"...","message":"..."}} in stderr or in the
// result's error text, falling back to the wrapped error.
func convertError(stderr, resultText string, err error) error {
	if msg, typ := extractCLIErrorInfo(stderr); msg != "" {
		return &provider.ProviderError{Type: typ, Message: msg, Err: err}
	}
	if msg, typ := extractCLIErrorInfo(resultText); msg != "" {
		return &provider.ProviderError{Type: typ, Message: msg, Err: err}
	}

	if s := strings.TrimSpace(stderr); s != "" {
		return &provider.ProviderError{Message: s, Err: err}
	}
	if s := strings.TrimSpace(resultText); s != "" {
		return &provider.ProviderError{Message: s, Err: err}
	}

	if err != nil {
		return &provider.ProviderError{Message: err.Error(), Err: err}
	}
	return errors.New("claude: unknown error")
}

func extractCLIErrorInfo(raw string) (msg, typ string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}

	// Walk lines; the first one that parses as a JSON error wins.
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}

		var payload struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		if payload.Error.Message != "" {
			return strings.TrimSpace(payload.Error.Message),
				strings.TrimSpace(payload.Error.Type)
		}
	}

	return "", ""
}

// convertUsage maps the CLI's usage block to provider.Usage. Returns nil for
// nil input so we don't synthesize zero-token usage.
func convertUsage(u *cliUsage) *provider.Usage {
	if u == nil {
		return nil
	}
	return &provider.Usage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
}

// convertStopReason maps CLI stop reasons to provider.CompletionStatus.
func convertStopReason(reason string) provider.CompletionStatus {
	switch reason {
	case "max_tokens":
		return provider.CompletionStatusIncomplete
	case "refusal":
		return provider.CompletionStatusRefused
	case "", "end_turn", "stop_sequence", "tool_use":
		return provider.CompletionStatusCompleted
	default:
		return provider.CompletionStatusCompleted
	}
}
