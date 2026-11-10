package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func TestResolveThinking(t *testing.T) {
	adaptive := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortMax, IncludeSummary: true}}
	disabled := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled, Effort: provider.EffortMax}}
	updates := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortLow, IncludeUpdates: true}}
	effortOnly := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Effort: provider.EffortLow}}
	summaryOnly := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{IncludeSummary: true}}
	updatesOnly := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{IncludeUpdates: true}}

	cases := []struct {
		name    string
		model   string
		options *provider.CompleteOptions
		forced  bool
		want    thinking
	}{
		{"adaptive", "claude-sonnet-4-6", adaptive, false, thinking{Enabled: true, Summarized: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"effort only leaves thinking alone", "claude-sonnet-4-6", effortOnly, false, thinking{Effort: anthropic.BetaOutputConfigEffortLow}},
		{"disabled", "claude-sonnet-4-6", disabled, false, thinking{Disabled: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"legacy ignores everything", "claude-sonnet-4-5", adaptive, false, thinking{}},
		{"forced tool disables", "claude-sonnet-4-6", adaptive, true, thinking{Disabled: true, Summarized: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"always-thinking cannot disable", "claude-fable-5-1", disabled, false, thinking{Effort: anthropic.BetaOutputConfigEffortMax}},
		{"fable 5.2 cannot disable", "claude-fable-5-2", disabled, false, thinking{Effort: anthropic.BetaOutputConfigEffortMax}},
		{"fable 5.2 updates", "claude-fable-5-2", updates, false, thinking{Enabled: true, Updates: true, Effort: anthropic.BetaOutputConfigEffortLow}},
		{"disabled effort is capped", "claude-opus-5", disabled, false, thinking{Disabled: true, Effort: anthropic.BetaOutputConfigEffortHigh}},
		{"opus 5.5 cannot disable", "claude-opus-5-5", disabled, false, thinking{Effort: anthropic.BetaOutputConfigEffortMax}},
		{"updates on a progress-update model", "claude-sonnet-5-5", updates, false, thinking{Enabled: true, Updates: true, Effort: anthropic.BetaOutputConfigEffortLow}},
		{"updates elsewhere are omitted", "claude-sonnet-4-6", updates, false, thinking{Enabled: true, Effort: anthropic.BetaOutputConfigEffortLow}},
		{"sonnet 5.5 disabled effort is capped", "claude-sonnet-5-5", disabled, false, thinking{Disabled: true, Effort: anthropic.BetaOutputConfigEffortHigh}},
		{"haiku 5.5 adaptive", "claude-haiku-5-5", adaptive, false, thinking{Enabled: true, Summarized: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"haiku 5.5 summary sets display", "claude-haiku-5-5", summaryOnly, false, thinking{Enabled: true, Summarized: true}},
		{"haiku 5.5 disabled effort is capped", "claude-haiku-5-5", disabled, false, thinking{Disabled: true, Effort: anthropic.BetaOutputConfigEffortHigh}},
		{"haiku 5.5 forced tool caps effort", "claude-haiku-5-5", adaptive, true, thinking{Disabled: true, Summarized: true, Effort: anthropic.BetaOutputConfigEffortHigh}},
		{"summary sets display on a default-thinking model", "claude-sonnet-5", summaryOnly, false, thinking{Enabled: true, Summarized: true}},
		{"sonnet 5.5 summary sets display", "claude-sonnet-5-5", summaryOnly, false, thinking{Enabled: true, Summarized: true}},
		{"opus 5.5 summary sets display", "claude-opus-5-5", summaryOnly, false, thinking{Enabled: true, Summarized: true}},
		{"updates set display on a default-thinking model", "claude-opus-5-5", updatesOnly, false, thinking{Enabled: true, Updates: true}},
		{"summary leaves an opt-in model off", "claude-opus-4-8", summaryOnly, false, thinking{Summarized: true}},
		{"forced tool keeps default display", "claude-fable-5", summaryOnly, true, thinking{Summarized: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Completer{Config: &Config{model: tc.model}}

			if got := c.resolveThinking(tc.options, tc.forced); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
