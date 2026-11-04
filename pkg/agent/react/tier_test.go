package react

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/stretchr/testify/require"
)

func TestTierPreferenceAndEffectiveTier(t *testing.T) {
	completer := &mockCompleter{responses: [][]provider.Completion{{
		{Tier: provider.TierStandard, Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("ok")}}},
		{Status: provider.CompletionStatusCompleted},
	}}}
	agent, err := New("test", WithCompleter(completer))
	require.NoError(t, err)
	options := &provider.CompleteOptions{Tier: provider.TierFlex}
	deltas, err := collectCompletions(agent.Complete(t.Context(), nil, options))
	require.NoError(t, err)
	require.Equal(t, provider.TierFlex, completer.capturedOptions[0].Tier)
	require.Equal(t, provider.TierFlex, options.Tier)
	var acc provider.CompletionAccumulator
	for _, delta := range deltas {
		acc.Add(*delta)
	}
	require.Equal(t, provider.TierStandard, acc.Result().Tier)
}
