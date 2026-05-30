package responses

import (
	"errors"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/server/openai/shared"
)

func TestToTools_RejectsWebSearch(t *testing.T) {
	in := []Tool{
		{Type: ToolTypeFunction, Name: "get_weather", Parameters: map[string]any{"type": "object"}},
		{Type: "web_search"},
	}

	_, err := toTools(in)
	if err == nil {
		t.Fatal("expected error for web_search")
	}

	var inv *shared.InvalidValueError
	if !errors.As(err, &inv) {
		t.Fatalf("expected InvalidValueError, got %T", err)
	}
	if inv.Param != "tools[1].type" {
		t.Errorf("Param = %q, want tools[1].type", inv.Param)
	}
	if !strings.Contains(inv.Message, "web_search") {
		t.Errorf("Message = %q", inv.Message)
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
