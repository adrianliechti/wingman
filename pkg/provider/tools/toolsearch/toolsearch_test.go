package toolsearch

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// A client-executed tool_search tool carries its own description and parameter
// schema upstream, so the emulation must forward them untouched rather than
// impose wingman's default shape.
// Source: ToolSearchToolParam (execution: "client"), openai-go v3.
func TestFunctionToolPreservesClientSchema(t *testing.T) {
	parameters := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"keywords": map[string]any{"type": "string"},
		},
		"required": []string{"keywords"},
	}

	tool := FunctionTool(provider.Tool{
		Kind:        provider.ToolKindToolSearch,
		Execution:   "client",
		Description: "find tools",
		Parameters:  parameters,
	})

	if tool.Kind != provider.ToolKindFunction {
		t.Fatalf("expected a plain function tool, got kind %q", tool.Kind)
	}

	if tool.Name != Name {
		t.Fatalf("expected the tool to keep the %q name, got %q", Name, tool.Name)
	}

	if tool.Description != "find tools" {
		t.Fatalf("expected the caller description to survive, got %q", tool.Description)
	}

	props, _ := tool.Parameters["properties"].(map[string]any)

	if _, ok := props["keywords"]; !ok {
		t.Fatalf("expected the caller schema to survive, got %v", tool.Parameters)
	}
}

// Upstream leaves both fields optional, so an omitted schema still has to
// produce something the model can call.
func TestFunctionToolFallsBackToQuerySchema(t *testing.T) {
	tool := FunctionTool(provider.Tool{Kind: provider.ToolKindToolSearch, Execution: "client"})

	if tool.Description == "" {
		t.Fatal("expected a fallback description")
	}

	props, _ := tool.Parameters["properties"].(map[string]any)

	if _, ok := props["query"]; !ok {
		t.Fatalf("expected a fallback query parameter, got %v", tool.Parameters)
	}

	required, _ := tool.Parameters["required"].([]string)

	if len(required) != 1 || required[0] != "query" {
		t.Errorf("expected query to be required, got %v", required)
	}
}
