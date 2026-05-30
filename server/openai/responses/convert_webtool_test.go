package responses

import "testing"

func TestToTools_DropsWebSearch(t *testing.T) {
	in := []Tool{
		{Type: ToolTypeFunction, Name: "get_weather", Parameters: map[string]any{"type": "object"}},
		{Type: "web_search"},
	}

	tools := toTools(in)

	if len(tools) != 1 {
		t.Fatalf("tools length = %d, want 1 (web_search should be dropped)", len(tools))
	}
	if tools[0].Name != "get_weather" {
		t.Errorf("remaining tool name = %q", tools[0].Name)
	}
}

func TestToTools_PassesThroughRegular(t *testing.T) {
	in := []Tool{{Type: ToolTypeFunction, Name: "weather", Parameters: map[string]any{"type": "object"}}}

	tools := toTools(in)

	if len(tools) != 1 {
		t.Fatalf("tools length = %d", len(tools))
	}
}
