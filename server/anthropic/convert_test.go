package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestToToolsTextEditor20250124(t *testing.T) {
	tools := []ToolParam{
		{
			Type: "text_editor_20250124",
			Name: "str_replace_editor",
		},
	}

	result := toTools(tools)

	require.Len(t, result, 1)
	require.Equal(t, provider.ToolTypeTextEditor, result[0].Type)
	require.Equal(t, "text_editor_20250124", result[0].Name)
}

func TestToToolsTextEditor20250429(t *testing.T) {
	tools := []ToolParam{
		{
			Type: "text_editor_20250429",
			Name: "str_replace_based_edit_tool",
		},
	}

	result := toTools(tools)

	require.Len(t, result, 1)
	require.Equal(t, provider.ToolTypeTextEditor, result[0].Type)
	require.Equal(t, "text_editor_20250429", result[0].Name)
}

func TestToToolsTextEditor20250728(t *testing.T) {
	tools := []ToolParam{
		{
			Type: "text_editor_20250728",
			Name: "str_replace_based_edit_tool",
		},
	}

	result := toTools(tools)

	require.Len(t, result, 1)
	require.Equal(t, provider.ToolTypeTextEditor, result[0].Type)
	require.Equal(t, "text_editor_20250728", result[0].Name)
}

func TestToToolsMixedFunctionAndTextEditor(t *testing.T) {
	tools := []ToolParam{
		{
			Name:        "get_weather",
			Description: "Get weather for a location",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type": "string",
					},
				},
			},
		},
		{
			Type: "text_editor_20250429",
			Name: "str_replace_based_edit_tool",
		},
	}

	result := toTools(tools)

	require.Len(t, result, 2)

	// First tool: function tool
	require.Equal(t, provider.ToolTypeFunction, result[0].Type)
	require.Equal(t, "get_weather", result[0].Name)
	require.Equal(t, "Get weather for a location", result[0].Description)

	// Second tool: text editor
	require.Equal(t, provider.ToolTypeTextEditor, result[1].Type)
	require.Equal(t, "text_editor_20250429", result[1].Name)
}

func TestToContentBlocksWithToolUse(t *testing.T) {
	// str_replace_editor tool calls come back as regular tool_use blocks
	content := []provider.Content{
		{
			Text: "I'll edit the file for you.",
		},
		{
			ToolCall: &provider.ToolCall{
				ID:        "toolu_abc123",
				Name:      "str_replace_editor",
				Arguments: `{"command":"str_replace","path":"test.py","old_str":"foo","new_str":"bar"}`,
			},
		},
	}

	blocks := toContentBlocks(content)

	require.Len(t, blocks, 2)

	// Text block
	require.Equal(t, "text", blocks[0].Type)
	require.Equal(t, "I'll edit the file for you.", blocks[0].Text)

	// Tool use block
	require.Equal(t, "tool_use", blocks[1].Type)
	require.Equal(t, "toolu_abc123", blocks[1].ID)
	require.Equal(t, "str_replace_editor", blocks[1].Name)
	require.NotNil(t, blocks[1].Input)

	input, ok := blocks[1].Input.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "str_replace", input["command"])
	require.Equal(t, "test.py", input["path"])
	require.Equal(t, "foo", input["old_str"])
	require.Equal(t, "bar", input["new_str"])
}

func TestToMessageWithToolResult(t *testing.T) {
	// Tool results for str_replace_editor are sent as regular tool_result blocks
	msg := MessageParam{
		Role: MessageRoleUser,
		Content: []any{
			map[string]any{
				"type":        "tool_result",
				"tool_use_id": "toolu_abc123",
				"content":     "File updated successfully.",
			},
		},
	}

	result, err := toMessage(msg)

	require.NoError(t, err)
	require.Equal(t, provider.MessageRoleUser, result.Role)
	require.Len(t, result.Content, 1)
	require.NotNil(t, result.Content[0].ToolResult)
	require.Equal(t, "toolu_abc123", result.Content[0].ToolResult.ID)
	require.Equal(t, "File updated successfully.", result.Content[0].ToolResult.Data)
}

func TestToMessageWithStrReplaceEditorToolUse(t *testing.T) {
	// Assistant message with str_replace_editor tool_use block
	msg := MessageParam{
		Role: MessageRoleAssistant,
		Content: []any{
			map[string]any{
				"type": "tool_use",
				"id":   "toolu_abc123",
				"name": "str_replace_editor",
				"input": map[string]any{
					"command": "view",
					"path":    "main.py",
				},
			},
		},
	}

	result, err := toMessage(msg)

	require.NoError(t, err)
	require.Equal(t, provider.MessageRoleAssistant, result.Role)
	require.Len(t, result.Content, 1)
	require.NotNil(t, result.Content[0].ToolCall)
	require.Equal(t, "toolu_abc123", result.Content[0].ToolCall.ID)
	require.Equal(t, "str_replace_editor", result.Content[0].ToolCall.Name)

	// Arguments should be JSON string
	require.Contains(t, result.Content[0].ToolCall.Arguments, "view")
	require.Contains(t, result.Content[0].ToolCall.Arguments, "main.py")
}

func TestToStopReasonWithToolUse(t *testing.T) {
	content := []provider.Content{
		{
			ToolCall: &provider.ToolCall{
				ID:   "toolu_abc123",
				Name: "str_replace_editor",
			},
		},
	}

	reason := toStopReason(content)
	require.Equal(t, StopReasonToolUse, reason)
}
