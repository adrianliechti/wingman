package openai

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
)

// TestResponderTools_ComputerNative verifies the OpenAI-dialect computer tool
// uses the native built-in on OpenAI hosts.
func TestResponderTools_ComputerNative(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindComputer, Name: computeruse.Name, Dialect: computeruse.DialectOpenAI}},
	}

	body := responsesRequestBody(t, responder, []provider.Message{provider.UserMessage("hi")}, options)

	tools := requestTools(t, body)
	if len(tools) != 1 || tools[0]["type"] != "computer" {
		t.Fatalf("tools: %+v", tools)
	}
}

// TestResponderTools_ComputerAnthropicEmulated verifies the Anthropic dialect
// is emulated as a function tool.
func TestResponderTools_ComputerAnthropicEmulated(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindComputer, Name: computeruse.Name, Dialect: computeruse.DialectAnthropic}},
	}

	body := responsesRequestBody(t, responder, []provider.Message{provider.UserMessage("hi")}, options)

	tools := requestTools(t, body)
	if len(tools) != 1 || tools[0]["type"] != "function" || tools[0]["name"] != computeruse.Name {
		t.Fatalf("tools: %+v", tools)
	}
}

// TestResponderInput_ComputerReplay verifies prior computer calls and results
// replay as computer_call / computer_call_output items, including safety
// checks.
func TestResponderInput_ComputerReplay(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	args := `{"call_id":"call_1","actions":[{"type":"click","button":"left","x":10,"y":20}],"pending_safety_checks":[{"id":"sc_1","code":"malicious_instructions","message":"verify"}]}`

	messages := []provider.Message{
		provider.UserMessage("click the button"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_1",
					Kind:      provider.ToolKindComputer,
					Name:      computeruse.Name,
					Arguments: args,
				}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{
					ID:      "call_1",
					Kind:    provider.ToolKindComputer,
					Payload: []byte(`[{"id":"sc_1","code":"malicious_instructions","message":"verify"}]`),
					Parts:   []provider.Part{{File: &provider.File{ContentType: "image/png", Content: []byte{1, 2, 3}}}},
				}),
			},
		},
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindComputer, Name: computeruse.Name, Dialect: computeruse.DialectOpenAI}},
	}

	body := responsesRequestBody(t, responder, messages, options)

	input := body["input"].([]any)

	var call, output map[string]any
	for _, item := range input {
		m := item.(map[string]any)
		switch m["type"] {
		case "computer_call":
			call = m
		case "computer_call_output":
			output = m
		case "function_call", "function_call_output":
			t.Fatalf("computer call replayed as function call: %+v", m)
		}
	}

	if call == nil || call["call_id"] != "call_1" {
		t.Fatalf("computer_call: %+v", call)
	}

	actions := call["actions"].([]any)
	if len(actions) != 1 || actions[0].(map[string]any)["type"] != "click" {
		t.Fatalf("actions: %+v", actions)
	}

	pending := call["pending_safety_checks"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["id"] != "sc_1" {
		t.Fatalf("pending_safety_checks: %+v", pending)
	}

	if output == nil || output["call_id"] != "call_1" {
		t.Fatalf("computer_call_output: %+v", output)
	}

	screenshot := output["output"].(map[string]any)
	if screenshot["type"] != "computer_screenshot" || screenshot["image_url"] == "" {
		t.Fatalf("screenshot: %+v", screenshot)
	}

	acked := output["acknowledged_safety_checks"].([]any)
	if len(acked) != 1 || acked[0].(map[string]any)["id"] != "sc_1" {
		t.Fatalf("acknowledged_safety_checks: %+v", acked)
	}
}
