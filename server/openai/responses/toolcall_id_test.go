package responses

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestResponseOutputsFunctionCallSplitsItemIDAndCallID(t *testing.T) {
	output := responseOutputs(&provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{
				ID:        "fc_123",
				CallID:    "call_123",
				Name:      "get_weather",
				Arguments: `{"city":"London"}`,
			}),
		},
	}, "msg_123", "completed", "", "")

	require.Len(t, output, 1)
	require.NotNil(t, output[0].FunctionCallOutputItem)
	require.Equal(t, "fc_123", output[0].FunctionCallOutputItem.ID)
	require.Equal(t, "call_123", output[0].FunctionCallOutputItem.CallID)
	require.Equal(t, "get_weather", output[0].FunctionCallOutputItem.Name)
}
