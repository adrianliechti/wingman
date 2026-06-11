package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
)

// TestConvertRequest_BashNative verifies the bash dialect uses the native
// bash tool, while the OpenAI shell dialects are emulated as function tools.
func TestConvertRequest_BashNative(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	tests := []struct {
		name     string
		wantType string
	}{
		{shell.NameBash, "bash_20250124"},
		{shell.NameShell, ""},
		{shell.NameLocalShell, ""},
	}

	for _, tt := range tests {
		options := &provider.CompleteOptions{
			Tools: []provider.Tool{{Kind: provider.ToolKindShell, Name: tt.name}},
		}

		body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

		tools := body["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("%s: expected 1 tool, got %d", tt.name, len(tools))
		}

		tool := tools[0].(map[string]any)

		if tt.wantType != "" {
			if tool["type"] != tt.wantType {
				t.Fatalf("%s: tool = %+v, want type %s", tt.name, tool, tt.wantType)
			}
			continue
		}

		if tool["type"] != nil && tool["type"] != "custom" {
			t.Fatalf("%s: expected a function tool, got %+v", tt.name, tool)
		}
		if tool["name"] != tt.name || tool["input_schema"] == nil {
			t.Fatalf("%s: emulated tool malformed: %+v", tt.name, tool)
		}
	}
}
