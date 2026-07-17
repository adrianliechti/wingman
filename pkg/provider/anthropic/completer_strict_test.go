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

// TestConvertRequest_ToolStrictSanitizesSchema verifies constraint keywords the
// strict validator rejects (minimum, maxLength, ...) are stripped from strict
// tool schemas, while properties that merely share a keyword name survive, and
// non-strict tools keep their schema untouched.
func TestConvertRequest_ToolStrictSanitizesSchema(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	strict := true

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"startLine": map[string]any{"type": []any{"integer", "null"}, "minimum": float64(1)},
			"name":      map[string]any{"type": "string", "maxLength": float64(10)},
			"minimum":   map[string]any{"type": "number"},
		},
		"required":             []string{"startLine", "name", "minimum"},
		"additionalProperties": false,
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Name: "read_file", Strict: &strict, Parameters: schema},
			{Name: "read_file_loose", Parameters: schema},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)

	strictProps := tools[0].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := strictProps["startLine"].(map[string]any)["minimum"]; ok {
		t.Errorf("minimum not stripped from strict schema: %+v", strictProps)
	}
	if _, ok := strictProps["name"].(map[string]any)["maxLength"]; ok {
		t.Errorf("maxLength not stripped from strict schema: %+v", strictProps)
	}
	if _, ok := strictProps["minimum"]; !ok {
		t.Errorf("property named 'minimum' wrongly stripped: %+v", strictProps)
	}

	looseProps := tools[1].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := looseProps["startLine"].(map[string]any)["minimum"]; !ok {
		t.Errorf("minimum wrongly stripped from non-strict schema: %+v", looseProps)
	}
}
