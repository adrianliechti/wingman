package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// TestConvertRequest_ToolStrict verifies an explicit strict flag on a function
// tool is passed through to the Anthropic tool definition, and that tools
// without the flag stay untouched.
func TestConvertRequest_ToolStrict(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	strict := true

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{
				Name:   "create_file",
				Strict: &strict,
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string"},
						"content": map[string]any{"type": "string"},
					},
					"required":             []string{"path", "content"},
					"additionalProperties": false,
				},
			},
			{
				Name:       "get_weather",
				Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	strictTool := tools[0].(map[string]any)
	if strictTool["strict"] != true {
		t.Errorf("strict not passed through: %+v", strictTool)
	}

	plainTool := tools[1].(map[string]any)
	if _, ok := plainTool["strict"]; ok {
		t.Errorf("strict unexpectedly set on unflagged tool: %+v", plainTool)
	}
}
