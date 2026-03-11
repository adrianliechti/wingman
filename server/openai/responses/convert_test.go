package responses

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/stretchr/testify/require"
)

// ── toToolOptions ────────────────────────────────────────────────────────────

func TestToToolOptions_Nil(t *testing.T) {
	require.Nil(t, toToolOptions(nil))
}

func TestToToolOptions_Auto(t *testing.T) {
	opts := toToolOptions(&ToolChoice{Mode: ToolChoiceModeAuto})
	require.Equal(t, provider.ToolChoiceAuto, opts.Choice)
	require.Empty(t, opts.Allowed)
}

func TestToToolOptions_None(t *testing.T) {
	opts := toToolOptions(&ToolChoice{Mode: ToolChoiceModeNone})
	require.Equal(t, provider.ToolChoiceNone, opts.Choice)
	require.Empty(t, opts.Allowed)
}

func TestToToolOptions_Required(t *testing.T) {
	opts := toToolOptions(&ToolChoice{Mode: ToolChoiceModeRequired})
	require.Equal(t, provider.ToolChoiceAny, opts.Choice)
	require.Empty(t, opts.Allowed)
}

func TestToToolOptions_SpecificFunction(t *testing.T) {
	opts := toToolOptions(&ToolChoice{
		Mode: ToolChoiceModeRequired,
		AllowedTools: []ToolChoiceAllowedTool{
			{Type: "function", Name: "get_weather"},
		},
	})
	require.Equal(t, provider.ToolChoiceAny, opts.Choice)
	require.Equal(t, []string{"get_weather"}, opts.Allowed)
}

func TestToToolOptions_AllowedList(t *testing.T) {
	opts := toToolOptions(&ToolChoice{
		Mode: ToolChoiceModeRequired,
		AllowedTools: []ToolChoiceAllowedTool{
			{Type: "function", Name: "get_weather"},
			{Type: "function", Name: "get_calendar"},
			{Type: "unknown", Name: "ignored"}, // non-function tools ignored
		},
	})
	require.Equal(t, provider.ToolChoiceAny, opts.Choice)
	require.Equal(t, []string{"get_weather", "get_calendar"}, opts.Allowed)
}

func TestToToolOptions_AllowedAutoMode(t *testing.T) {
	opts := toToolOptions(&ToolChoice{
		Mode: ToolChoiceModeAuto,
		AllowedTools: []ToolChoiceAllowedTool{
			{Type: "function", Name: "get_weather"},
		},
	})
	require.Equal(t, provider.ToolChoiceAuto, opts.Choice)
	require.Equal(t, []string{"get_weather"}, opts.Allowed)
}

// ── toTools ──────────────────────────────────────────────────────────────────

func TestToToolsApplyPatch(t *testing.T) {
	tools := []Tool{
		{
			Type: ToolTypeApplyPatch,
		},
	}

	result := toTools(tools)

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

	result := toTools(tools)

	require.Len(t, result, 2)

	require.Equal(t, "get_weather", result[0].Name)

	require.Equal(t, provider.ToolTypeTextEditor, result[1].Type)
	require.Equal(t, "apply_patch", result[1].Name)
}

// ── toMessages ───────────────────────────────────────────────────────────────

func userItem(text string) InputItem {
	return InputItem{
		Type: InputItemTypeMessage,
		InputMessage: &InputMessage{
			Role:    MessageRoleUser,
			Content: []InputContent{{Type: InputContentText, Text: text}},
		},
	}
}

func assistantItem(text string) InputItem {
	return InputItem{
		Type: InputItemTypeMessage,
		InputMessage: &InputMessage{
			Role:    MessageRoleAssistant,
			Content: []InputContent{{Type: InputContentText, Text: text}},
		},
	}
}

func functionCallItem(callID, name, arguments string) InputItem {
	return InputItem{
		Type: InputItemTypeFunctionCall,
		InputFunctionCall: &InputFunctionCall{
			CallID:    callID,
			Name:      name,
			Arguments: arguments,
		},
	}
}

func functionCallOutputItem(callID, output string) InputItem {
	return InputItem{
		Type: InputItemTypeFunctionCallOutput,
		InputFunctionCallOutput: &InputFunctionCallOutput{
			CallID: callID,
			Output: output,
		},
	}
}

func reasoningItem(id, signature string) InputItem {
	return InputItem{
		Type: InputItemTypeReasoning,
		InputReasoning: &InputReasoning{
			ID:               id,
			EncryptedContent: signature,
		},
	}
}

func TestToMessages_Empty(t *testing.T) {
	msgs, err := toMessages(nil, "")
	require.NoError(t, err)
	require.Empty(t, msgs)
}

func TestToMessages_InstructionsOnly(t *testing.T) {
	msgs, err := toMessages(nil, "Be helpful")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, provider.MessageRoleSystem, msgs[0].Role)
	require.Equal(t, "Be helpful", msgs[0].Content[0].Text)
}

func TestToMessages_SingleUserMessage(t *testing.T) {
	msgs, err := toMessages([]InputItem{userItem("Hello")}, "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, provider.MessageRoleUser, msgs[0].Role)
	require.Equal(t, "Hello", msgs[0].Content[0].Text)
}

func TestToMessages_InstructionsPrependedBeforeItems(t *testing.T) {
	msgs, err := toMessages([]InputItem{userItem("Hi")}, "You are helpful")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, provider.MessageRoleSystem, msgs[0].Role)
	require.Equal(t, "You are helpful", msgs[0].Content[0].Text)
	require.Equal(t, provider.MessageRoleUser, msgs[1].Role)
}

func TestToMessages_MultiTurn(t *testing.T) {
	items := []InputItem{
		userItem("Hello"),
		assistantItem("Hi there!"),
		userItem("How are you?"),
	}
	msgs, err := toMessages(items, "")
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	require.Equal(t, provider.MessageRoleUser, msgs[0].Role)
	require.Equal(t, provider.MessageRoleAssistant, msgs[1].Role)
	require.Equal(t, provider.MessageRoleUser, msgs[2].Role)
}

func TestToMessages_SingleFunctionCallRound(t *testing.T) {
	items := []InputItem{
		userItem("What's the weather?"),
		functionCallItem("call_1", "get_weather", `{"city":"London"}`),
		functionCallOutputItem("call_1", "Sunny, 22°C"),
	}
	msgs, err := toMessages(items, "")
	require.NoError(t, err)
	// user, assistant (tool call), user (tool result)
	require.Len(t, msgs, 3)

	require.Equal(t, provider.MessageRoleUser, msgs[0].Role)

	require.Equal(t, provider.MessageRoleAssistant, msgs[1].Role)
	require.Len(t, msgs[1].Content, 1)
	require.NotNil(t, msgs[1].Content[0].ToolCall)
	require.Equal(t, "call_1", msgs[1].Content[0].ToolCall.ID)
	require.Equal(t, "get_weather", msgs[1].Content[0].ToolCall.Name)

	require.Equal(t, provider.MessageRoleUser, msgs[2].Role)
	require.Len(t, msgs[2].Content, 1)
	require.NotNil(t, msgs[2].Content[0].ToolResult)
	require.Equal(t, "call_1", msgs[2].Content[0].ToolResult.ID)
	require.Equal(t, "Sunny, 22°C", msgs[2].Content[0].ToolResult.Data)
}

func TestToMessages_ParallelFunctionCalls(t *testing.T) {
	// Multiple consecutive function_call items -> single assistant message with multiple tool calls
	// Multiple consecutive function_call_output items -> single user message with multiple tool results
	items := []InputItem{
		userItem("Compare weather in London and Paris"),
		functionCallItem("call_1", "get_weather", `{"city":"London"}`),
		functionCallItem("call_2", "get_weather", `{"city":"Paris"}`),
		functionCallOutputItem("call_1", "Sunny"),
		functionCallOutputItem("call_2", "Rainy"),
	}
	msgs, err := toMessages(items, "")
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	// Single assistant message with both tool calls
	require.Equal(t, provider.MessageRoleAssistant, msgs[1].Role)
	require.Len(t, msgs[1].Content, 2)
	require.Equal(t, "call_1", msgs[1].Content[0].ToolCall.ID)
	require.Equal(t, "call_2", msgs[1].Content[1].ToolCall.ID)

	// Single user message with both tool results
	require.Equal(t, provider.MessageRoleUser, msgs[2].Role)
	require.Len(t, msgs[2].Content, 2)
	require.Equal(t, "call_1", msgs[2].Content[0].ToolResult.ID)
	require.Equal(t, "call_2", msgs[2].Content[1].ToolResult.ID)
}

func TestToMessages_ReasoningMergedIntoAssistantMessage(t *testing.T) {
	// reasoning item immediately before an assistant message -> merged into that message
	items := []InputItem{
		userItem("Think carefully"),
		reasoningItem("rs_1", "encrypted_sig"),
		assistantItem("The answer is 42"),
	}
	msgs, err := toMessages(items, "")
	require.NoError(t, err)
	// user, assistant (reasoning + text)
	require.Len(t, msgs, 2)

	require.Equal(t, provider.MessageRoleAssistant, msgs[1].Role)
	require.Len(t, msgs[1].Content, 2)
	require.NotNil(t, msgs[1].Content[0].Reasoning, "first content should be reasoning")
	require.Equal(t, "rs_1", msgs[1].Content[0].Reasoning.ID)
	require.Equal(t, "encrypted_sig", msgs[1].Content[0].Reasoning.Signature)
	require.Equal(t, "The answer is 42", msgs[1].Content[1].Text)
}

func TestToMessages_ReasoningFlushedWithFunctionCalls(t *testing.T) {
	// reasoning + function_call items -> single assistant message: [reasoning, call]
	items := []InputItem{
		userItem("Use a tool"),
		reasoningItem("rs_1", "sig"),
		functionCallItem("call_1", "get_weather", `{}`),
	}
	msgs, err := toMessages(items, "")
	require.NoError(t, err)
	// user, assistant (reasoning + tool call)
	require.Len(t, msgs, 2)

	require.Equal(t, provider.MessageRoleAssistant, msgs[1].Role)
	require.Len(t, msgs[1].Content, 2)
	require.NotNil(t, msgs[1].Content[0].Reasoning)
	require.NotNil(t, msgs[1].Content[1].ToolCall)
}

func TestToMessages_DeveloperRoleMapsToSystem(t *testing.T) {
	items := []InputItem{
		{
			Type: InputItemTypeMessage,
			InputMessage: &InputMessage{
				Role:    MessageRoleDeveloper,
				Content: []InputContent{{Type: InputContentText, Text: "Be precise"}},
			},
		},
		userItem("Hello"),
	}
	msgs, err := toMessages(items, "")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, provider.MessageRoleSystem, msgs[0].Role)
}

func TestToMessages_FullConversationWithToolUse(t *testing.T) {
	// Full multi-turn: user -> [tool call] -> [tool result] -> assistant reply -> user follow-up
	items := []InputItem{
		userItem("What's the weather in London?"),
		functionCallItem("call_1", "get_weather", `{"city":"London"}`),
		functionCallOutputItem("call_1", "Sunny, 22°C"),
		assistantItem("It's sunny and 22°C in London."),
		userItem("And in Paris?"),
	}
	msgs, err := toMessages(items, "Be helpful")
	require.NoError(t, err)
	// system, user, assistant(call), user(result), assistant(text), user
	require.Len(t, msgs, 6)
	require.Equal(t, provider.MessageRoleSystem, msgs[0].Role)
	require.Equal(t, provider.MessageRoleUser, msgs[1].Role)
	require.Equal(t, provider.MessageRoleAssistant, msgs[2].Role)
	require.NotNil(t, msgs[2].Content[0].ToolCall)
	require.Equal(t, provider.MessageRoleUser, msgs[3].Role)
	require.NotNil(t, msgs[3].Content[0].ToolResult)
	require.Equal(t, provider.MessageRoleAssistant, msgs[4].Role)
	require.Equal(t, "It's sunny and 22°C in London.", msgs[4].Content[0].Text)
	require.Equal(t, provider.MessageRoleUser, msgs[5].Role)
}

// ── apply_patch toMessages tests ─────────────────────────────────────────────

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

// ── apply_patch marshal/unmarshal tests ──────────────────────────────────────

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
