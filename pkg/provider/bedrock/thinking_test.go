package bedrock

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
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
		{"adaptive", "eu.anthropic.claude-sonnet-4-6", nil, adaptive, false, thinking{Enabled: true, Summarized: true, Effort: "max"}},
		{"effort only leaves thinking alone", "eu.anthropic.claude-sonnet-4-6", nil, effortOnly, false, thinking{Effort: "low"}},
		{"disabled", "eu.anthropic.claude-sonnet-4-6", nil, disabled, false, thinking{Disabled: true, Effort: "max"}},
		{"legacy ignores everything", "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", nil, adaptive, false, thinking{}},
		{"forced tool disables", "eu.anthropic.claude-sonnet-4-6", nil, adaptive, true, thinking{Disabled: true, Summarized: true, Effort: "max"}},
		{"unsigned tool turn disables", "eu.anthropic.claude-sonnet-4-6", unsigned, adaptive, false, thinking{Disabled: true, Summarized: true, Effort: "max"}},
		{"disabled effort is capped", "eu.anthropic.claude-opus-5", nil, disabled, false, thinking{Disabled: true, Effort: "high"}},
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
