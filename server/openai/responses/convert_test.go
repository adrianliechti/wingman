package responses

import (
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
	// Multiple consecutive function_call items → single assistant message with multiple tool calls
	// Multiple consecutive function_call_output items → single user message with multiple tool results
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
	// reasoning item immediately before an assistant message → merged into that message
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
	// reasoning + function_call items → single assistant message: [reasoning, call]
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
	// Full multi-turn: user → [tool call] → [tool result] → assistant reply → user follow-up
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
