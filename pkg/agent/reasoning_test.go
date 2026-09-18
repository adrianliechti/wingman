package agent_test

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/agent/assistant"
	"github.com/adrianliechti/wingman/pkg/agent/react"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/stretchr/testify/require"
)

type reasoningCapture struct {
	options *provider.ReasoningOptions
}

func (c *reasoningCapture) Complete(_ context.Context, _ []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		c.options = options.ReasoningOptions
		message := provider.AssistantMessage("done")
		yield(&provider.Completion{Message: &message, Reasoning: provider.ReasoningContextCurrentTurn}, nil)
	}
}

func TestAgentReasoningDefaultsPreserveCallerOptions(t *testing.T) {
	for _, kind := range []string{"assistant", "react"} {
		for _, tc := range []struct {
			name    string
			options *provider.ReasoningOptions
			effort  provider.Effort
		}{
			{name: "omitted", effort: provider.EffortLow},
			{name: "signature", options: &provider.ReasoningOptions{IncludeSignature: true}, effort: provider.EffortLow},
			{name: "summary and retention", options: &provider.ReasoningOptions{IncludeSignature: true, IncludeSummary: true, Context: provider.ReasoningContextAllTurns}, effort: provider.EffortLow},
			{name: "explicit effort", options: &provider.ReasoningOptions{Effort: provider.EffortHigh, IncludeSignature: true}, effort: provider.EffortHigh},
			{name: "disabled", options: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled, IncludeSignature: true}},
			{name: "adaptive", options: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, IncludeSummary: true}},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				capture := &reasoningCapture{}
				var completer provider.Completer
				var err error
				if kind == "assistant" {
					completer, err = assistant.New("agent", assistant.WithCompleter(capture), assistant.WithEffort(provider.EffortLow))
				} else {
					completer, err = react.New("agent", react.WithCompleter(capture), react.WithEffort(provider.EffortLow))
				}
				require.NoError(t, err)
				var before provider.ReasoningOptions
				if tc.options != nil {
					before = *tc.options
				}
				options := &provider.CompleteOptions{ReasoningOptions: tc.options}
				for completion, err := range completer.Complete(t.Context(), nil, options) {
					require.NoError(t, err)
					require.Equal(t, provider.ReasoningContextCurrentTurn, completion.Reasoning)
				}
				want := before
				want.Effort = tc.effort
				require.Equal(t, &want, capture.options)
				require.Same(t, tc.options, options.ReasoningOptions)
				if tc.options != nil {
					require.Equal(t, before, *tc.options, "caller reasoning options were mutated")
				}
			})
		}
	}
}
