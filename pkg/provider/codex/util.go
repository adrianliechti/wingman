package codex

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// convertError surfaces a clean error from the codex stderr stream when no
// JSON-RPC error is available. Falls back to wrapping err.
func convertError(stderr, msg string, err error) error {
	if s := strings.TrimSpace(msg); s != "" {
		return &provider.ProviderError{Message: s, Err: err}
	}
	if s := strings.TrimSpace(stderr); s != "" {
		return &provider.ProviderError{Message: s, Err: err}
	}
	if err != nil {
		return &provider.ProviderError{Message: err.Error(), Err: err}
	}
	return errors.New("codex: unknown error")
}

// convertUsage maps the codex token-usage breakdown to provider.Usage.
// Returns nil for nil input so we don't synthesize zero-token usage.
func convertUsage(u *tokenUsageBreakdown) *provider.Usage {
	if u == nil {
		return nil
	}
	return &provider.Usage{
		InputTokens:          int(u.InputTokens),
		OutputTokens:         int(u.OutputTokens) + int(u.ReasoningOutputTokens),
		CacheReadInputTokens: int(u.CachedInputTokens),
	}
}

// convertTurnStatus maps codex turn statuses to provider.CompletionStatus.
func convertTurnStatus(status string) provider.CompletionStatus {
	switch strings.ToLower(status) {
	case "completed":
		return provider.CompletionStatusCompleted
	case "interrupted":
		return provider.CompletionStatusIncomplete
	case "failed":
		return provider.CompletionStatusFailed
	case "inprogress", "in_progress":
		return provider.CompletionStatusCompleted
	}
	return provider.CompletionStatusCompleted
}

// effortString maps a provider.Effort to the codex `effort` enum value.
// codex understands minimal/low/medium/high; we collapse the rest sensibly.
func effortString(eff provider.Effort) string {
	switch eff {
	case provider.EffortNone:
		return "minimal"
	case provider.EffortMinimal:
		return "minimal"
	case provider.EffortLow:
		return "low"
	case provider.EffortMedium:
		return "medium"
	case provider.EffortHigh:
		return "high"
	case provider.EffortXHigh, provider.EffortMax:
		return "high"
	}
	return ""
}
