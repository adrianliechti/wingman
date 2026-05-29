package bedrock

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

var testSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title": map[string]any{"type": "string"},
	},
	"required":             []string{"title"},
	"additionalProperties": false,
}

func TestConvertConverseInputUsesForcedToolForSchema(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-opus-4-8-v1:0"}}

	req, err := c.convertConverseInput([]provider.Message{
		provider.UserMessage("Return JSON."),
	}, &provider.CompleteOptions{
		Schema: &provider.Schema{
			Name:       "classify_chat",
			Properties: testSchema,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if req.ToolConfig == nil {
		t.Fatal("expected tool config for schema mode")
	}

	choice, ok := req.ToolConfig.ToolChoice.(*types.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("expected forced tool choice, got %T", req.ToolConfig.ToolChoice)
	}
	if got := *choice.Value.Name; got != "classify_chat" {
		t.Fatalf("expected forced tool %q, got %q", "classify_chat", got)
	}

	var found bool
	for _, tool := range req.ToolConfig.Tools {
		spec, ok := tool.(*types.ToolMemberToolSpec)
		if ok && *spec.Value.Name == "classify_chat" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected schema tool in tool config")
	}
}
