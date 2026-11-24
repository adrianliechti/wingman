package react

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/stretchr/testify/require"
)

// The agent rebuilds every chunk, so completion-level metadata reaches the
// caller only if it is copied explicitly.
func TestCompleteForwardsTurnMetadata(t *testing.T) {
	reasoning := func(id, signature string) provider.Completion {
		return provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{ID: id, Signature: signature}),
		}}}
	}
	completer := &mockCompleter{responses: [][]provider.Completion{
		{
			reasoning("rs_1", "streamed-1"),
			{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{ID: "call-a", Name: "tool_a", Arguments: `{}`}),
			}}},
			{
				Status: provider.CompletionStatusCompleted, EndTurn: new(false),
				InputTransformations: &[]provider.InputTransformation{{ID: "rs_caller", Action: "dropped"}},
				ReasoningSnapshots:   provider.ReasoningSnapshots{"rs_1": {ID: "rs_1", Signature: "final-1"}},
			},
		},
		{
			reasoning("rs_2", "streamed-2"),
			{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("Done")}}},
			{
				Status: provider.CompletionStatusCompleted, EndTurn: new(true),
				// Reports the agent's own item from the first turn.
				InputTransformations: &[]provider.InputTransformation{{ID: "rs_1", Action: "dropped"}},
				ReasoningSnapshots:   provider.ReasoningSnapshots{"rs_2": {ID: "rs_2", Signature: "final-2"}},
			},
		},
	}}
	chain, err := New("test-model", WithCompleter(completer), WithTools(&mockToolProvider{tools: []provider.Tool{{Name: "tool_a"}}}))
	require.NoError(t, err)

	completions, err := collectCompletions(chain.Complete(context.Background(), nil, nil))
	require.NoError(t, err)
	require.Equal(t, 2, completer.callCount)
	for _, completion := range completions[:len(completions)-1] {
		require.Nil(t, completion.EndTurn, "the internal tool round's boundary surfaced")
	}

	acc := provider.CompletionAccumulator{}
	for _, completion := range completions {
		acc.Add(*completion)
	}
	result := acc.Result()

	var signatures []string
	for _, content := range result.Message.Content {
		if r := content.Reasoning; r != nil {
			signatures = append(signatures, r.ID+"="+r.Signature)
		}
	}
	require.Equal(t, []string{"rs_1=final-1", "rs_2=final-2"}, signatures, "terminal reasoning copies from every turn apply")
	require.NotNil(t, result.EndTurn)
	require.True(t, *result.EndTurn)
	require.Equal(t, &[]provider.InputTransformation{{ID: "rs_caller", Action: "dropped"}}, result.InputTransformations)
}
