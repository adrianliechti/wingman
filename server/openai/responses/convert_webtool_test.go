package responses

import (
	"testing"
)

func TestToTools_SkipsHostedWebSearch(t *testing.T) {
	in := []Tool{
		{Type: ToolTypeFunction, Name: "get_weather", Parameters: map[string]any{"type": "object"}},
		{Type: ToolTypeWebSearch},
	}

	tools, err := toTools(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "get_weather" {
		t.Fatalf("web_search was not skipped cleanly: %+v", tools)
	}
}

func TestToTools_PassesThroughRegular(t *testing.T) {
	in := []Tool{{Type: ToolTypeFunction, Name: "weather", Parameters: map[string]any{"type": "object"}}}

	tools, err := toTools(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools length = %d", len(tools))
	}
}
