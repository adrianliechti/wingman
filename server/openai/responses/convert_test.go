package responses

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestToToolsApplyPatch(t *testing.T) {
	tools := []Tool{
		{
			Type: ToolTypeApplyPatch,
		},
	}

	result, err := toTools(tools)

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, provider.ToolTypeTextEditor, result[0].Type)
	require.Equal(t, "apply_patch", result[0].Name)
}

func TestToToolsMixedFunctionAndApplyPatch(t *testing.T) {
	tools := []Tool{
		{
			Type:        ToolTypeFunction,
			Name:        "get_weather",
			Description: "Get weather",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{"type": "string"},
				},
			},
		},
		{
			Type: ToolTypeApplyPatch,
		},
	}

	result, err := toTools(tools)

	require.NoError(t, err)
	require.Len(t, result, 2)

	require.Equal(t, provider.ToolTypeFunction, result[0].Type)
	require.Equal(t, "get_weather", result[0].Name)

	require.Equal(t, provider.ToolTypeTextEditor, result[1].Type)
	require.Equal(t, "apply_patch", result[1].Name)
}

func TestToMessagesApplyPatchCall(t *testing.T) {
	items := []InputItem{
		{
			Type: InputItemTypeMessage,
			InputMessage: &InputMessage{
				Role: MessageRoleUser,
				Content: []InputContent{
					{Type: InputContentText, Text: "Fix the bug in main.py"},
				},
			},
		},
		{
			Type: InputItemTypeApplyPatchCall,
			InputApplyPatchCall: &InputApplyPatchCall{
				ID:     "patch_001",
				CallID: "call_abc123",
				Patch:  "--- a/main.py\n+++ b/main.py\n@@ -1,3 +1,3 @@\n-print('hello')\n+print('world')\n",
				Status: "completed",
			},
		},
	}

	messages, err := toMessages(items, "")

	require.NoError(t, err)
	require.Len(t, messages, 2)

	// First: user message
	require.Equal(t, provider.MessageRoleUser, messages[0].Role)
	require.Equal(t, "Fix the bug in main.py", messages[0].Content[0].Text)

	// Second: assistant message with tool call
	require.Equal(t, provider.MessageRoleAssistant, messages[1].Role)
	require.NotNil(t, messages[1].Content[0].ToolCall)
	require.Equal(t, "call_abc123", messages[1].Content[0].ToolCall.ID)
	require.Equal(t, "apply_patch", messages[1].Content[0].ToolCall.Name)
	require.Contains(t, messages[1].Content[0].ToolCall.Arguments, "print('world')")
}

func TestToMessagesApplyPatchCallOutput(t *testing.T) {
	items := []InputItem{
		{
			Type: InputItemTypeApplyPatchCallOutput,
			InputApplyPatchCallOutput: &InputApplyPatchCallOutput{
				CallID: "call_abc123",
				Output: "Patch applied successfully.",
				Status: "completed",
			},
		},
	}

	messages, err := toMessages(items, "")

	require.NoError(t, err)
	require.Len(t, messages, 1)

	require.Equal(t, provider.MessageRoleUser, messages[0].Role)
	require.NotNil(t, messages[0].Content[0].ToolResult)
	require.Equal(t, "call_abc123", messages[0].Content[0].ToolResult.ID)
	require.Equal(t, "Patch applied successfully.", messages[0].Content[0].ToolResult.Data)
}

func TestToMessagesApplyPatchMultiTurn(t *testing.T) {
	items := []InputItem{
		{
			Type: InputItemTypeMessage,
			InputMessage: &InputMessage{
				Role: MessageRoleUser,
				Content: []InputContent{
					{Type: InputContentText, Text: "Fix the typo"},
				},
			},
		},
		{
			Type: InputItemTypeApplyPatchCall,
			InputApplyPatchCall: &InputApplyPatchCall{
				CallID: "call_1",
				Patch:  "--- a/file.py\n+++ b/file.py\n@@ -1 +1 @@\n-pritn\n+print\n",
			},
		},
		{
			Type: InputItemTypeApplyPatchCallOutput,
			InputApplyPatchCallOutput: &InputApplyPatchCallOutput{
				CallID: "call_1",
				Output: "Patch applied successfully.",
				Status: "completed",
			},
		},
		{
			Type: InputItemTypeMessage,
			InputMessage: &InputMessage{
				Role: MessageRoleAssistant,
				Content: []InputContent{
					{Type: InputContentText, Text: "I've fixed the typo."},
				},
			},
		},
	}

	messages, err := toMessages(items, "")

	require.NoError(t, err)
	require.Len(t, messages, 4)

	// User message
	require.Equal(t, provider.MessageRoleUser, messages[0].Role)

	// Assistant tool call
	require.Equal(t, provider.MessageRoleAssistant, messages[1].Role)
	require.NotNil(t, messages[1].Content[0].ToolCall)
	require.Equal(t, "apply_patch", messages[1].Content[0].ToolCall.Name)

	// Tool result
	require.Equal(t, provider.MessageRoleUser, messages[2].Role)
	require.NotNil(t, messages[2].Content[0].ToolResult)

	// Assistant text
	require.Equal(t, provider.MessageRoleAssistant, messages[3].Role)
	require.Equal(t, "I've fixed the typo.", messages[3].Content[0].Text)
}

func TestResponsesInputUnmarshalApplyPatchCall(t *testing.T) {
	input := `[
		{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Fix the bug"}]},
		{"type": "apply_patch_call", "id": "patch_1", "call_id": "call_1", "patch": "--- a/f.py\n+++ b/f.py\n", "status": "completed"},
		{"type": "apply_patch_call_output", "call_id": "call_1", "output": "OK", "status": "completed"}
	]`

	var ri ResponsesInput
	err := json.Unmarshal([]byte(input), &ri)

	require.NoError(t, err)
	require.Len(t, ri.Items, 3)

	require.Equal(t, InputItemTypeMessage, ri.Items[0].Type)
	require.NotNil(t, ri.Items[0].InputMessage)

	require.Equal(t, InputItemTypeApplyPatchCall, ri.Items[1].Type)
	require.NotNil(t, ri.Items[1].InputApplyPatchCall)
	require.Equal(t, "patch_1", ri.Items[1].InputApplyPatchCall.ID)
	require.Equal(t, "call_1", ri.Items[1].InputApplyPatchCall.CallID)
	require.Contains(t, ri.Items[1].InputApplyPatchCall.Patch, "--- a/f.py")

	require.Equal(t, InputItemTypeApplyPatchCallOutput, ri.Items[2].Type)
	require.NotNil(t, ri.Items[2].InputApplyPatchCallOutput)
	require.Equal(t, "call_1", ri.Items[2].InputApplyPatchCallOutput.CallID)
	require.Equal(t, "OK", ri.Items[2].InputApplyPatchCallOutput.Output)
}

func TestResponseOutputMarshalApplyPatchCall(t *testing.T) {
	output := ResponseOutput{
		Type: ResponseOutputTypeApplyPatchCall,
		ApplyPatchCallOutputItem: &ApplyPatchCallOutputItem{
			ID:     "patch_1",
			Type:   "apply_patch_call",
			Status: "completed",
			CallID: "call_1",
			Patch:  "--- a/f.py\n+++ b/f.py\n",
		},
	}

	data, err := json.Marshal(output)
	require.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	require.Equal(t, "apply_patch_call", result["type"])
	require.Equal(t, "patch_1", result["id"])
	require.Equal(t, "completed", result["status"])
	require.Equal(t, "call_1", result["call_id"])
	require.Contains(t, result["patch"], "--- a/f.py")
}
