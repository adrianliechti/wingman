package openai

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
)

// TestResponderTools_ShellNative verifies the OpenAI shell dialects use the
// native tools on OpenAI hosts, while the bash dialect is emulated.
func TestResponderTools_ShellNative(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	tests := []struct {
		name     string
		wantType string
	}{
		{shell.NameShell, "shell"},
		{shell.NameLocalShell, "local_shell"},
		{shell.NameBash, "function"},
	}

	for _, tt := range tests {
		options := &provider.CompleteOptions{
			Tools: []provider.Tool{{Kind: provider.ToolKindShell, Name: tt.name}},
		}

		body := responsesRequestBody(t, responder, []provider.Message{provider.UserMessage("hi")}, options)

		tools := requestTools(t, body)
		if len(tools) != 1 || tools[0]["type"] != tt.wantType {
			t.Fatalf("%s: tools = %+v, want type %s", tt.name, tools, tt.wantType)
		}
	}
}

// TestResponderInput_ShellReplay verifies prior shell calls and results
// replay as shell_call / shell_call_output items.
func TestResponderInput_ShellReplay(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	messages := []provider.Message{
		provider.UserMessage("run the tests"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_1",
					Kind:      provider.ToolKindShell,
					Name:      shell.NameShell,
					Arguments: `{"commands":["go test ./..."],"timeout_ms":60000}`,
				}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{
					ID:      "call_1",
					Kind:    provider.ToolKindShell,
					Parts:   []provider.Part{{Text: "ok"}},
					Payload: []byte(`{"type":"shell_call_output","output":[{"stdout":"ok","outcome":{"type":"exit","exit_code":0}}]}`),
				}),
			},
		},
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindShell, Name: shell.NameShell}},
	}

	body := responsesRequestBody(t, responder, messages, options)

	input := body["input"].([]any)

	var call, output map[string]any
	for _, item := range input {
		m := item.(map[string]any)
		switch m["type"] {
		case "shell_call":
			call = m
		case "shell_call_output":
			output = m
		case "function_call", "function_call_output":
			t.Fatalf("shell call replayed as function call: %+v", m)
		}
	}

	if call == nil || call["call_id"] != "call_1" {
		t.Fatalf("shell_call: %+v", call)
	}

	action := call["action"].(map[string]any)
	commands := action["commands"].([]any)
	if len(commands) != 1 || commands[0] != "go test ./..." {
		t.Fatalf("action: %+v", action)
	}

	if output == nil || output["call_id"] != "call_1" {
		t.Fatalf("shell_call_output: %+v", output)
	}

	chunks := output["output"].([]any)
	if len(chunks) != 1 || chunks[0].(map[string]any)["stdout"] != "ok" {
		t.Fatalf("output chunks: %+v", chunks)
	}
}
