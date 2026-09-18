package react

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestComplete_ToolSearchExecutionOwnership(t *testing.T) {
	for _, execution := range []string{"server", "", "client"} {
		t.Run(execution, func(t *testing.T) {
			search := provider.ToolCallContent(provider.ToolCall{ID: "search", Name: "tool_search", Kind: provider.ToolKindToolSearch, Execution: execution, Arguments: `{}`})
			result := provider.ToolResultContent(provider.ToolResult{ID: "search", Kind: provider.ToolKindToolSearch, Execution: execution, Payload: []byte(`[{"type":"function","name":"lookup"}]`)})
			lookup := provider.ToolCallContent(provider.ToolCall{ID: "lookup", Name: "lookup", Arguments: `{}`})
			content := []provider.Content{search, result, lookup}
			if execution == "client" {
				content = []provider.Content{search, lookup}
			}
			backend := &mockCompleter{responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: content}}},
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("done")}}}},
			}}
			tools := &mockToolProvider{tools: []provider.Tool{{Name: "lookup"}}}
			chain, err := New("agent", WithCompleter(backend), WithTools(tools))
			require.NoError(t, err)
			completion, err := accumulateCompletion(chain.Complete(t.Context(), []provider.Message{provider.UserMessage("Look up status")}, nil))
			if execution == "client" {
				require.ErrorContains(t, err, "both agent-handled and caller-handled tool calls")
				require.Empty(t, tools.executeCalls)
				require.Equal(t, 1, backend.callCount)
				return
			}
			require.NoError(t, err)
			require.Len(t, tools.executeCalls, 1)
			require.Equal(t, "lookup", tools.executeCalls[0].Name)
			require.Equal(t, 2, backend.callCount)
			require.Equal(t, content, backend.capturedMessages[1][1].Content, "hosted search must remain in replay history")
			require.Equal(t, "lookup", backend.capturedMessages[1][2].Content[0].ToolResult.ID)
			require.Equal(t, []provider.Content{search, result, provider.TextContent("done")}, completion.Message.Content, "hosted events must reach the client without exposing the agent-owned call")
		})
	}
}
