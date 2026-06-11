package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
)

// TestConvertRequest_TextEditorNative verifies the Anthropic-dialect text
// editor tool is sent as the native text_editor_20250728 tool, including the
// optional max_characters.
func TestConvertRequest_TextEditorNative(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Kind: provider.ToolKindTextEditor, Name: texteditor.NameTextEditor, MaxCharacters: 10000},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != "text_editor_20250728" || tool["name"] != texteditor.NameTextEditor {
		t.Fatalf("tool: %+v", tool)
	}
	if tool["max_characters"] != float64(10000) {
		t.Errorf("max_characters: %v", tool["max_characters"])
	}
}

// TestConvertRequest_TextEditorApplyPatchEmulated verifies the OpenAI
// apply_patch dialect is emulated as a plain function tool, so calls and
// results stay in the client's dialect end-to-end.
func TestConvertRequest_TextEditorApplyPatchEmulated(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Kind: provider.ToolKindTextEditor, Name: texteditor.NameApplyPatch},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != nil && tool["type"] != "custom" {
		t.Fatalf("expected a function tool, got type %v", tool["type"])
	}
	if tool["name"] != texteditor.NameApplyPatch {
		t.Fatalf("tool name: %v", tool["name"])
	}
	if tool["input_schema"] == nil || tool["description"] == nil {
		t.Fatalf("emulated tool missing schema or description: %+v", tool)
	}
}
