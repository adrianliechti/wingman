package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func TestToResponseUsageDistinguishesMissingReasoningFromZero(t *testing.T) {
	for _, tc := range []struct {
		name, details string
		known         bool
		tokens        int
	}{
		{"missing breakdown", "", false, 0},
		{"missing count", `,"output_tokens_details":{}`, false, 0},
		{"null count", `,"output_tokens_details":{"reasoning_tokens":null}`, false, 0},
		{"measured zero", `,"output_tokens_details":{"reasoning_tokens":0}`, true, 0},
		{"measured reasoning", `,"output_tokens_details":{"reasoning_tokens":3}`, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw responses.ResponseUsage
			if err := json.Unmarshal([]byte(`{"input_tokens":10,"output_tokens":5,"total_tokens":15`+tc.details+`}`), &raw); err != nil {
				t.Fatal(err)
			}
			usage := toResponseUsage(raw)
			if usage == nil || usage.HasReasoningTokens() != tc.known || tc.known && *usage.ReasoningTokens != tc.tokens {
				t.Fatalf("usage = %+v, want known=%t tokens=%d", usage, tc.known, tc.tokens)
			}
			var chatRaw openai.CompletionUsage
			details := strings.ReplaceAll(tc.details, "output_tokens_details", "completion_tokens_details")
			if err := json.Unmarshal([]byte(`{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15`+details+`}`), &chatRaw); err != nil {
				t.Fatal(err)
			}
			chatUsage := toUsage(chatRaw)
			if chatUsage == nil || chatUsage.HasReasoningTokens() != tc.known || tc.known && *chatUsage.ReasoningTokens != tc.tokens {
				t.Fatalf("chat usage = %+v, want known=%t tokens=%d", chatUsage, tc.known, tc.tokens)
			}
		})
	}
}

func TestUsagePreservesReportedZeroWithoutOtherTokens(t *testing.T) {
	var raw responses.ResponseUsage
	if err := json.Unmarshal([]byte(`{"output_tokens_details":{"reasoning_tokens":0}}`), &raw); err != nil {
		t.Fatal(err)
	}
	if usage := toResponseUsage(raw); !usage.HasReasoningTokens() || *usage.ReasoningTokens != 0 {
		t.Fatalf("usage = %+v, want measured zero", usage)
	}
	var chatRaw openai.CompletionUsage
	if err := json.Unmarshal([]byte(`{"completion_tokens_details":{"reasoning_tokens":0}}`), &chatRaw); err != nil {
		t.Fatal(err)
	}
	if usage := toUsage(chatRaw); !usage.HasReasoningTokens() || *usage.ReasoningTokens != 0 {
		t.Fatalf("chat usage = %+v, want measured zero", usage)
	}
}

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
	if usage.ReasoningTokens == nil {
		t.Fatal("expected reasoning token count")
	}
	if *usage.ReasoningTokens != 3 {
		t.Errorf("ReasoningTokens = %d, want 3", *usage.ReasoningTokens)
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
