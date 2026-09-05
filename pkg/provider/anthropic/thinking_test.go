package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/anthropics/anthropic-sdk-go"
)

func TestResolveThinking(t *testing.T) {
	adaptive := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeAdaptive, Effort: provider.EffortMax, IncludeSummary: true}}
	disabled := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled, Effort: provider.EffortMax}}
	effortOnly := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Effort: provider.EffortLow}}

	unsigned := []provider.Message{
		provider.UserMessage("hi"),
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "t", Name: "f", Arguments: "{}"})}},
		provider.ToolMessage("t", "ok"),
	}

	cases := []struct {
		name     string
		model    string
		messages []provider.Message
		options  *provider.CompleteOptions
		forced   bool
		want     thinking
	}{
		{"adaptive", "claude-sonnet-4-6", nil, adaptive, false, thinking{Enabled: true, Summarized: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"effort only leaves thinking alone", "claude-sonnet-4-6", nil, effortOnly, false, thinking{Effort: anthropic.BetaOutputConfigEffortLow}},
		{"disabled", "claude-sonnet-4-6", nil, disabled, false, thinking{Disabled: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"legacy ignores everything", "claude-sonnet-4-5", nil, adaptive, false, thinking{}},
		{"forced tool disables", "claude-sonnet-4-6", nil, adaptive, true, thinking{Disabled: true, Summarized: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"unsigned tool turn disables", "claude-sonnet-4-6", unsigned, adaptive, false, thinking{Disabled: true, Summarized: true, Effort: anthropic.BetaOutputConfigEffortMax}},
		{"always-thinking cannot disable", "claude-fable-5-1", nil, disabled, false, thinking{Effort: anthropic.BetaOutputConfigEffortMax}},
		{"disabled effort is capped", "claude-opus-5", nil, disabled, false, thinking{Disabled: true, Effort: anthropic.BetaOutputConfigEffortHigh}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Completer{Config: &Config{model: tc.model}}

			if got := c.resolveThinking(tc.messages, tc.options, tc.forced); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
