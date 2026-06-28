package chat

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func intPtr(v int) *int { return &v }

// TestToCompleteOptions_MaxTokensFallback verifies the deprecated max_tokens
// parameter is honored when max_completion_tokens is absent.
func TestToCompleteOptions_MaxTokensFallback(t *testing.T) {
	options := toCompleteOptions(ChatCompletionRequest{MaxTokens: intPtr(512)}, nil)

	if options.MaxTokens == nil || *options.MaxTokens != 512 {
		t.Fatalf("expected max tokens 512, got %v", options.MaxTokens)
	}
}

func TestToCompleteOptions_MaxCompletionTokensPrecedence(t *testing.T) {
	options := toCompleteOptions(ChatCompletionRequest{
		MaxCompletionTokens: intPtr(1024),
		MaxTokens:           intPtr(512),
	}, nil)

	if options.MaxTokens == nil || *options.MaxTokens != 1024 {
		t.Fatalf("expected max_completion_tokens to win, got %v", options.MaxTokens)
	}
}

func TestToCompleteOptions_Stop(t *testing.T) {
	options := toCompleteOptions(ChatCompletionRequest{Stop: "END"}, nil)
	if len(options.Stop) != 1 || options.Stop[0] != "END" {
		t.Fatalf("string stop: got %v", options.Stop)
	}

	options = toCompleteOptions(ChatCompletionRequest{Stop: []any{"A", "B"}}, nil)
	if len(options.Stop) != 2 || options.Stop[0] != "A" || options.Stop[1] != "B" {
		t.Fatalf("array stop: got %v", options.Stop)
	}
}

// reasoning_effort must convert to provider.ReasoningOptions so the Anthropic
// provider emits adaptive thinking: any real effort selects ReasoningTypeAdaptive
// (never the deprecated manual budget mode that Opus 4.8/4.7 reject), "none"
// disables thinking, and an absent field leaves reasoning unset.
func TestToCompleteOptions_ReasoningEffort(t *testing.T) {
	cases := []struct {
		effort     ReasoningEffort
		wantType   provider.ReasoningType
		wantEffort provider.Effort
	}{
		{ReasoningEffortMinimal, provider.ReasoningTypeAdaptive, provider.EffortMinimal},
		{ReasoningEffortLow, provider.ReasoningTypeAdaptive, provider.EffortLow},
		{ReasoningEffortMedium, provider.ReasoningTypeAdaptive, provider.EffortMedium},
		{ReasoningEffortHigh, provider.ReasoningTypeAdaptive, provider.EffortHigh},
		{ReasoningEffortXHigh, provider.ReasoningTypeAdaptive, provider.EffortXHigh},
		{ReasoningEffortMax, provider.ReasoningTypeAdaptive, provider.EffortMax},
		{ReasoningEffortNone, provider.ReasoningTypeDisabled, ""},
	}

	for _, tc := range cases {
		t.Run(string(tc.effort), func(t *testing.T) {
			options := toCompleteOptions(ChatCompletionRequest{ReasoningEffort: tc.effort}, nil)
			if options.ReasoningOptions == nil {
				t.Fatalf("expected ReasoningOptions for effort %q", tc.effort)
			}
			if options.ReasoningOptions.Type != tc.wantType {
				t.Errorf("effort %q: type = %q, want %q", tc.effort, options.ReasoningOptions.Type, tc.wantType)
			}
			if options.ReasoningOptions.Effort != tc.wantEffort {
				t.Errorf("effort %q: effort = %q, want %q", tc.effort, options.ReasoningOptions.Effort, tc.wantEffort)
			}
		})
	}

	if options := toCompleteOptions(ChatCompletionRequest{}, nil); options.ReasoningOptions != nil {
		t.Errorf("absent reasoning_effort: expected nil ReasoningOptions, got %+v", options.ReasoningOptions)
	}
}
