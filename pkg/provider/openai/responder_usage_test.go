package openai

import (
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

// TestToResponseUsage_CacheInclusiveInputTokens verifies that the Responses
// API's input_tokens (already cache-inclusive) maps straight to InputTokens,
// with cached_tokens exposed as the cached subset and reasoning tokens carried
// through.
func TestToResponseUsage_CacheInclusiveInputTokens(t *testing.T) {
	usage := toResponseUsage(responses.ResponseUsage{
		InputTokens:  100,
		OutputTokens: 7,
		TotalTokens:  107,
		InputTokensDetails: responses.ResponseUsageInputTokensDetails{
			CachedTokens: 40,
		},
		OutputTokensDetails: responses.ResponseUsageOutputTokensDetails{
			ReasoningTokens: 3,
		},
	})

	if usage == nil {
		t.Fatal("expected usage")
	}

	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (cache-inclusive input_tokens)", usage.InputTokens)
	}
	if usage.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", usage.OutputTokens)
	}
	if usage.ReasoningTokens != 3 {
		t.Errorf("ReasoningTokens = %d, want 3", usage.ReasoningTokens)
	}
	if usage.CacheReadInputTokens != 40 {
		t.Errorf("CacheReadInputTokens = %d, want 40", usage.CacheReadInputTokens)
	}

	if usage.CacheReadInputTokens > usage.InputTokens {
		t.Errorf("cache read tokens (%d) exceed InputTokens (%d)", usage.CacheReadInputTokens, usage.InputTokens)
	}
}

func TestToResponseUsage_ZeroReturnsNil(t *testing.T) {
	if usage := toResponseUsage(responses.ResponseUsage{}); usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
}
