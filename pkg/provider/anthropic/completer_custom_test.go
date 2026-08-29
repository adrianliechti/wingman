package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"
)

// The Messages API has no freeform tool type, so a custom tool is emulated as
// a function tool with a single string parameter. Before that it fell through
// to the generic path and was declared with an empty schema, which asked the
// model for JSON instead of freeform text.
func TestConvertRequest_CustomToolEmulated(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Kind: provider.ToolKindCustom, Name: "run_python", Description: "Run a script."},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools, _ := body["tools"].([]any)

	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool, _ := tools[0].(map[string]any)

	if tool["name"] != "run_python" {
		t.Fatalf("expected the tool to keep its name, got %v", tool["name"])
	}

	schema, _ := tool["input_schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)

	if len(props) != 1 {
		t.Fatalf("expected exactly one parameter, got %v", props)
	}

	input, _ := props[custom.InputParameter].(map[string]any)

	if input == nil || input["type"] != "string" {
		t.Fatalf("expected a string %q parameter, got %v", custom.InputParameter, props)
	}
}

// A replayed custom tool call carries raw freeform text; it has to be
// re-encoded into the emulated schema or the tool_use block loses its input.
func TestConvertRequest_CustomToolCallReplayKeepsFreeformInput(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	source := "print(\"alpha — beta\")\nx = r\"C:\\temp\\z.txt\"\n"

	messages := []provider.Message{
		provider.UserMessage("run it"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_1",
					Kind:      provider.ToolKindCustom,
					Name:      "run_python",
					Arguments: source,
				}),
			},
		},
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindCustom, Name: "run_python"}},
	}

	body := requestBody(t, completer, messages, options)

	var use map[string]any

	for _, m := range body["messages"].([]any) {
		message, _ := m.(map[string]any)
		content, _ := message["content"].([]any)

		for _, block := range content {
			obj, _ := block.(map[string]any)

			if obj["type"] == "tool_use" {
				use = obj
			}
		}
	}

	if use == nil {
		t.Fatal("no tool_use block in the converted request")
	}

	input, _ := use["input"].(map[string]any)
	got, _ := input[custom.InputParameter].(string)

	if got != source {
		t.Fatalf("freeform input did not survive replay:\n want %q\n got  %q", source, got)
	}
}
