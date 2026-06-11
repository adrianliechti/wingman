package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
)

// TestConvertRequest_ComputerNative verifies the Anthropic-dialect computer
// tool is sent as the native computer use tool with display dimensions.
func TestConvertRequest_ComputerNative(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{
				Kind:    provider.ToolKindComputer,
				Name:    computeruse.Name,
				Dialect: computeruse.DialectAnthropic,
				Display: &provider.Display{Width: 1280, Height: 800},
			},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != "computer_20251124" || tool["name"] != computeruse.Name {
		t.Fatalf("tool: %+v", tool)
	}
	if tool["display_width_px"] != float64(1280) || tool["display_height_px"] != float64(800) {
		t.Fatalf("display: %+v", tool)
	}
}

// TestConvertRequest_ComputerOpenAIEmulated verifies the OpenAI dialect is
// emulated as a plain function tool.
func TestConvertRequest_ComputerOpenAIEmulated(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{
				Kind:    provider.ToolKindComputer,
				Name:    computeruse.Name,
				Dialect: computeruse.DialectOpenAI,
				Display: &provider.Display{Width: 1280, Height: 800},
			},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != nil && tool["type"] != "custom" {
		t.Fatalf("expected a function tool, got type %v", tool["type"])
	}
	if tool["name"] != computeruse.Name || tool["input_schema"] == nil {
		t.Fatalf("tool: %+v", tool)
	}
}
