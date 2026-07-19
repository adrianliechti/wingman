package bedrock

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// TestConvertToolConfig_Strict verifies an explicit strict flag on a function
// tool is passed through to the Converse tool specification, and that tools
// without the flag stay untouched.
func TestConvertToolConfig_Strict(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-opus-4-8-v1:0"}}

	strict := true

	tc := c.convertToolConfig([]provider.Tool{
		{Name: "create_file", Strict: &strict, Parameters: testSchema},
		{Name: "get_weather", Parameters: testSchema},
	}, nil)

	specs := map[string]types.ToolSpecification{}
	for _, tool := range tc.Tools {
		if spec, ok := tool.(*types.ToolMemberToolSpec); ok {
			specs[*spec.Value.Name] = spec.Value
		}
	}

	if spec := specs["create_file"]; spec.Strict == nil || !*spec.Strict {
		t.Errorf("strict not passed through: %+v", spec)
	}
	if spec := specs["get_weather"]; spec.Strict != nil {
		t.Errorf("strict unexpectedly set on unflagged tool: %+v", spec)
	}
}

// TestConvertToolConfig_StrictFalse verifies an explicit strict=false is not
// forwarded: Anthropic models on Bedrock reject the unknown tool field with
// "tools.0.custom.strict: Extra inputs are not permitted".
func TestConvertToolConfig_StrictFalse(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-opus-4-8-v1:0"}}

	strict := false

	tc := c.convertToolConfig([]provider.Tool{
		{Name: "search_agent", Strict: &strict, Parameters: testSchema},
	}, nil)

	for _, tool := range tc.Tools {
		if spec, ok := tool.(*types.ToolMemberToolSpec); ok {
			if spec.Value.Strict != nil {
				t.Errorf("strict=false unexpectedly forwarded: %+v", spec.Value)
			}
		}
	}
}

// TestConvertConverseInput_SchemaStrict verifies schema-mode strict propagates
// to the forced structured-output tool.
func TestConvertConverseInput_SchemaStrict(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-opus-4-8-v1:0"}}

	strict := true

	req, err := c.convertConverseInput([]provider.Message{
		provider.UserMessage("Return JSON."),
	}, &provider.CompleteOptions{
		Schema: &provider.Schema{
			Name:       "classify_chat",
			Strict:     &strict,
			Properties: testSchema,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range req.ToolConfig.Tools {
		if spec, ok := tool.(*types.ToolMemberToolSpec); ok && *spec.Value.Name == "classify_chat" {
			if spec.Value.Strict == nil || !*spec.Value.Strict {
				t.Fatalf("strict not set on schema tool: %+v", spec.Value)
			}
			return
		}
	}

	t.Fatal("schema tool not found in tool config")
}
