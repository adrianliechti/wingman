package google

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"

	"google.golang.org/genai"
)

// Gemini has no freeform tool type, so a custom tool is emulated as a function
// declaration with a single string parameter.
func TestConvertTools_CustomEmulated(t *testing.T) {
	tools, err := convertTools([]provider.Tool{
		{Kind: provider.ToolKindCustom, Name: "run_python", Description: "Run a script."},
	})

	if err != nil {
		t.Fatalf("a custom tool must be emulated, not rejected: %v", err)
	}

	if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected one function declaration, got %+v", tools)
	}

	declaration := tools[0].FunctionDeclarations[0]

	if declaration.Name != "run_python" {
		t.Fatalf("expected the tool to keep its name, got %q", declaration.Name)
	}

	// Gemini takes the raw JSON schema, not the typed genai.Schema.
	schema, _ := declaration.ParametersJsonSchema.(map[string]any)
	props, _ := schema["properties"].(map[string]any)

	if len(props) != 1 {
		t.Fatalf("expected exactly one parameter, got %v", props)
	}

	input, _ := props[custom.InputParameter].(map[string]any)

	if input == nil || input["type"] != "string" {
		t.Fatalf("expected a string %q parameter, got %v", custom.InputParameter, props)
	}
}

// Gemini delivers complete arguments, so the emulated wrapper is unwrapped in
// place — the client must see freeform text, not {"input": ...}.
func TestToContent_CustomToolCallUnwrapsInput(t *testing.T) {
	source := "print(\"alpha — beta\")\n"

	content := &genai.Content{
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{
				Name: "run_python",
				Args: map[string]any{custom.InputParameter: source},
			}},
		},
	}

	parts := toContent(content, nil, []provider.Tool{
		{Kind: provider.ToolKindCustom, Name: "run_python"},
	})

	for _, p := range parts {
		if p.ToolCall == nil {
			continue
		}

		if p.ToolCall.Kind != provider.ToolKindCustom {
			t.Errorf("expected the call to be tagged custom, got %q", p.ToolCall.Kind)
		}

		if p.ToolCall.Arguments != source {
			t.Fatalf("expected unwrapped freeform input:\n want %q\n got  %q", source, p.ToolCall.Arguments)
		}

		return
	}

	t.Fatal("no tool call in the converted content")
}
