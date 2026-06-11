package openai

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
)

func responsesRequestBody(t *testing.T, responder *Responder, messages []provider.Message, options *provider.CompleteOptions) map[string]any {
	t.Helper()

	req, err := responder.convertResponsesRequest(messages, options)
	if err != nil {
		t.Fatalf("convertResponsesRequest: %v", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	return body
}

func requestTools(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	raw, _ := body["tools"].([]any)

	var tools []map[string]any
	for _, item := range raw {
		tools = append(tools, item.(map[string]any))
	}
	return tools
}

// TestResponderTools_ApplyPatchNative verifies the apply_patch dialect uses
// the native built-in tool on OpenAI hosts.
func TestResponderTools_ApplyPatchNative(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindTextEditor, Name: texteditor.NameApplyPatch}},
	}

	body := responsesRequestBody(t, responder, []provider.Message{provider.UserMessage("hi")}, options)

	tools := requestTools(t, body)
	if len(tools) != 1 || tools[0]["type"] != "apply_patch" {
		t.Fatalf("tools: %+v", tools)
	}
}

// TestResponderTools_TextEditorEmulated verifies the Anthropic dialect is
// emulated as a function tool so calls stay in the client's dialect.
func TestResponderTools_TextEditorEmulated(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindTextEditor, Name: texteditor.NameTextEditor}},
	}

	body := responsesRequestBody(t, responder, []provider.Message{provider.UserMessage("hi")}, options)

	tools := requestTools(t, body)
	if len(tools) != 1 || tools[0]["type"] != "function" || tools[0]["name"] != texteditor.NameTextEditor {
		t.Fatalf("tools: %+v", tools)
	}
	if tools[0]["parameters"] == nil {
		t.Fatalf("emulated tool missing parameters: %+v", tools[0])
	}
}

// TestResponderTools_ApplyPatchEmulatedOffOpenAI verifies third-party
// OpenAI-compatible hosts (xAI, proxies) never receive the built-in tool.
func TestResponderTools_ApplyPatchEmulatedOffOpenAI(t *testing.T) {
	responder, _ := NewResponder("https://api.x.ai/v1/", "grok-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindTextEditor, Name: texteditor.NameApplyPatch}},
	}

	body := responsesRequestBody(t, responder, []provider.Message{provider.UserMessage("hi")}, options)

	tools := requestTools(t, body)
	if len(tools) != 1 || tools[0]["type"] != "function" || tools[0]["name"] != texteditor.NameApplyPatch {
		t.Fatalf("tools: %+v", tools)
	}
}

// TestResponderInput_ApplyPatchReplay verifies prior apply_patch calls and
// results replay as apply_patch_call / apply_patch_call_output items rather
// than function calls.
func TestResponderInput_ApplyPatchReplay(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	args := `{"type":"update_file","path":"main.go","diff":"@@\n-old\n+new\n"}`

	messages := []provider.Message{
		provider.UserMessage("update main.go"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_1",
					Kind:      provider.ToolKindTextEditor,
					Name:      texteditor.NameApplyPatch,
					Arguments: args,
				}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{
					ID:    "call_1",
					Kind:  provider.ToolKindTextEditor,
					Parts: []provider.Part{{Text: "Updated main.go"}},
				}),
			},
		},
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindTextEditor, Name: texteditor.NameApplyPatch}},
	}

	body := responsesRequestBody(t, responder, messages, options)

	input := body["input"].([]any)

	var call, output map[string]any
	for _, item := range input {
		m := item.(map[string]any)
		switch m["type"] {
		case "apply_patch_call":
			call = m
		case "apply_patch_call_output":
			output = m
		case "function_call", "function_call_output":
			t.Fatalf("text editor call replayed as function call: %+v", m)
		}
	}

	if call == nil || call["call_id"] != "call_1" {
		t.Fatalf("apply_patch_call: %+v", call)
	}

	op := call["operation"].(map[string]any)
	if op["type"] != "update_file" || op["path"] != "main.go" || op["diff"] != "@@\n-old\n+new\n" {
		t.Fatalf("operation: %+v", op)
	}

	if output == nil || output["call_id"] != "call_1" || output["output"] != "Updated main.go" {
		t.Fatalf("apply_patch_call_output: %+v", output)
	}
	if output["status"] != "completed" {
		t.Fatalf("output status: %v", output["status"])
	}
}

// TestResponderInput_TextEditorReplayAsFunction verifies Anthropic-dialect
// history (emulated function tool) replays as plain function call items.
func TestResponderInput_TextEditorReplayAsFunction(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	messages := []provider.Message{
		provider.UserMessage("update main.go"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_1",
					Kind:      provider.ToolKindTextEditor,
					Name:      texteditor.NameTextEditor,
					Arguments: `{"command":"str_replace","path":"main.go","old_str":"old","new_str":"new"}`,
				}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{
					ID:    "call_1",
					Parts: []provider.Part{{Text: "ok"}},
				}),
			},
		},
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindTextEditor, Name: texteditor.NameTextEditor}},
	}

	body := responsesRequestBody(t, responder, messages, options)

	input := body["input"].([]any)

	var sawCall, sawOutput bool
	for _, item := range input {
		m := item.(map[string]any)
		switch m["type"] {
		case "function_call":
			sawCall = true
			if m["name"] != texteditor.NameTextEditor {
				t.Fatalf("function_call name: %v", m["name"])
			}
		case "function_call_output":
			sawOutput = true
		case "apply_patch_call", "apply_patch_call_output":
			t.Fatalf("emulated call replayed as built-in item: %+v", m)
		}
	}

	if !sawCall || !sawOutput {
		t.Fatalf("missing function call/output: call=%v output=%v", sawCall, sawOutput)
	}
}
