package bedrock

import (
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

var noneHistory = []provider.Message{
	provider.UserMessage("Check it"),
	{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "check", Name: "check", Arguments: "{}"})}},
	{Role: provider.MessageRoleUser, Content: []provider.Content{provider.ToolResultContent(provider.ToolResult{ID: "check", Parts: []provider.Part{{Text: "PASS"}}})}},
}

var noneTools = []provider.Tool{{Name: "check", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}

// Claude's native "none" returns an empty answer where the model would have
// called a tool, so "none" leaves the tools out. Tool history needs their
// definitions, so it keeps the request as it is, cached prefix included.
func TestConverseNoneLeavesToolsOut(t *testing.T) {
	for _, model := range []string{"anthropic.claude-sonnet-5", "anthropic.claude-opus-5-5", "anthropic.claude-haiku-4-5", "amazon.nova-pro-v1:0"} {
		t.Run(model, func(t *testing.T) {
			c := &Completer{Config: &Config{model: model}}

			automatic, err := c.convertConverseInput(noneHistory, &provider.CompleteOptions{Tools: noneTools})
			if err != nil {
				t.Fatal(err)
			}

			options := &provider.CompleteOptions{Tools: noneTools, ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceNone}}
			disabled, err := c.convertConverseInput(noneHistory, options)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(automatic, disabled) {
				t.Fatal("none with tool history changed the request")
			}
			if options.ToolOptions.Choice != provider.ToolChoiceNone {
				t.Fatal("mutated caller options")
			}

			plain, err := c.convertConverseInput([]provider.Message{provider.UserMessage("hello")}, options)
			if err != nil {
				t.Fatal(err)
			}
			if plain.ToolConfig != nil {
				t.Fatal("offered tools without history under none")
			}
		})
	}
}

// A schema emulated as a tool can be forced by name, which excludes the
// client tools even with tool history. Models without forced tool choice
// select the schema tool themselves.
func TestConverseNoneForcesSchemaTool(t *testing.T) {
	for model, forced := range map[string]bool{"anthropic.claude-opus-4-7": true, "anthropic.claude-opus-5-5": false} {
		t.Run(model, func(t *testing.T) {
			c := &Completer{Config: &Config{model: model}}

			req, err := c.convertConverseInput(noneHistory, &provider.CompleteOptions{
				Tools:       noneTools,
				ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceNone},
				Schema:      &provider.Schema{Name: "answer", Properties: testSchema},
			})
			if err != nil {
				t.Fatal(err)
			}

			switch choice := req.ToolConfig.ToolChoice.(type) {
			case *types.ToolChoiceMemberTool:
				if !forced || aws.ToString(choice.Value.Name) != "answer" {
					t.Fatalf("tool choice = %q", aws.ToString(choice.Value.Name))
				}
			case *types.ToolChoiceMemberAuto:
				if forced {
					t.Fatal("schema tool not forced")
				}
			default:
				t.Fatalf("tool choice = %T", choice)
			}
		})
	}
}
