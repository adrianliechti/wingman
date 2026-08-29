package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Converse has no freeform tool type, so a custom tool has to be emulated as a
// function tool with a single string parameter. Before that, convertToolConfig
// rejected it outright with UnsupportedToolError.
func TestConvertToolConfig_CustomEmulated(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6-v1:0"}}

	tc, err := c.convertToolConfig([]provider.Tool{
		{Kind: provider.ToolKindCustom, Name: "run_python", Description: "Run a script."},
	}, nil)

	if err != nil {
		t.Fatalf("a custom tool must be emulated, not rejected: %v", err)
	}

	var spec *types.ToolSpecification

	for _, tool := range tc.Tools {
		if member, ok := tool.(*types.ToolMemberToolSpec); ok && *member.Value.Name == "run_python" {
			spec = &member.Value
		}
	}

	if spec == nil {
		t.Fatal("run_python missing from the tool config")
	}

	if spec.Strict != nil {
		t.Error("the emulated schema must not request strict validation — Bedrock ignores it on Claude models")
	}

	schema, ok := spec.InputSchema.(*types.ToolInputSchemaMemberJson)

	if !ok {
		t.Fatal("expected a JSON input schema")
	}

	params, _ := schema.Value.MarshalSmithyDocument()

	var decoded map[string]any

	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props, _ := decoded["properties"].(map[string]any)

	if len(props) != 1 {
		t.Fatalf("expected exactly one parameter, got %v", props)
	}

	if _, ok := props[custom.InputParameter]; !ok {
		t.Fatalf("expected the %q parameter, got %v", custom.InputParameter, props)
	}
}

// Regression test for silent data loss: a replayed custom tool call carries
// raw freeform text, which fails json.Unmarshal. The completer substituted an
// empty map, so the model saw a call it had supposedly made with no input.
func TestConvertConverseInput_CustomToolCallReplayKeepsFreeformInput(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6-v1:0"}}

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

	input, err := c.convertConverseInput(messages, options)

	if err != nil {
		t.Fatalf("convertConverseInput: %v", err)
	}

	var use *types.ToolUseBlock

	for _, m := range input.Messages {
		for _, block := range m.Content {
			if b, ok := block.(*types.ContentBlockMemberToolUse); ok {
				use = &b.Value
			}
		}
	}

	if use == nil {
		t.Fatal("no tool_use block in the converted request")
	}

	data, _ := use.Input.MarshalSmithyDocument()

	var decoded map[string]any

	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("tool_use input is not valid JSON: %v", err)
	}

	got, _ := decoded[custom.InputParameter].(string)

	if got != source {
		t.Fatalf("freeform input did not survive replay:\n want %q\n got  %q", source, got)
	}
}

// A custom tool call that already carries the wrapper (an emulated call being
// replayed straight back) must not be wrapped twice.
func TestConvertConverseInput_CustomToolCallReplayDoesNotDoubleWrap(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6-v1:0"}}

	messages := []provider.Message{
		provider.UserMessage("run it"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_1",
					Kind:      provider.ToolKindCustom,
					Name:      "run_python",
					Arguments: custom.Wrap("print(1)"),
				}),
			},
		},
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindCustom, Name: "run_python"}},
	}

	input, err := c.convertConverseInput(messages, options)

	if err != nil {
		t.Fatalf("convertConverseInput: %v", err)
	}

	for _, m := range input.Messages {
		for _, block := range m.Content {
			b, ok := block.(*types.ContentBlockMemberToolUse)

			if !ok {
				continue
			}

			data, _ := b.Value.Input.MarshalSmithyDocument()

			var decoded map[string]any
			json.Unmarshal(data, &decoded)

			if got, _ := decoded[custom.InputParameter].(string); got != "print(1)" {
				t.Fatalf("expected the wrapper to be reused, got %q", got)
			}
		}
	}
}
