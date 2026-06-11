package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func boolPtr(v bool) *bool { return &v }

// TestConvertRequest_ToolSearchNative verifies a server-executed tool_search
// maps to the native Anthropic tool search tool and deferred tools keep their
// defer_loading flag.
func TestConvertRequest_ToolSearchNative(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Kind: provider.ToolKindToolSearch, Name: "tool_search_tool_regex", Execution: "server"},
			{Name: "get_weather", Parameters: map[string]any{"type": "object"}, Deferred: boolPtr(true)},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(tools), tools)
	}

	search := tools[0].(map[string]any)
	if search["type"] != "tool_search_tool_regex_20251119" {
		t.Fatalf("search tool: %+v", search)
	}

	weather := tools[1].(map[string]any)
	if weather["name"] != "get_weather" || weather["defer_loading"] != true {
		t.Fatalf("deferred tool: %+v", weather)
	}
}

// TestConvertRequest_ToolSearchClientEmulated verifies a client-executed
// tool_search becomes a plain function tool, and deferred tools stay loaded
// (no native search tool could discover them).
func TestConvertRequest_ToolSearchClientEmulated(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Kind: provider.ToolKindToolSearch, Execution: "client", Description: "find tools"},
			{Name: "get_weather", Parameters: map[string]any{"type": "object"}, Deferred: boolPtr(true)},
		},
	}

	body := requestBody(t, completer, []provider.Message{provider.UserMessage("hi")}, options)

	tools := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(tools), tools)
	}

	search := tools[0].(map[string]any)
	if search["type"] != nil && search["type"] != "custom" {
		t.Fatalf("expected a function tool, got %+v", search)
	}
	if search["name"] != "tool_search" || search["input_schema"] == nil {
		t.Fatalf("emulated search tool: %+v", search)
	}

	weather := tools[1].(map[string]any)
	if weather["defer_loading"] == true {
		t.Fatalf("tool deferred without a native search tool: %+v", weather)
	}
}

// TestConvertRequest_ToolSearchDiscoveredTools verifies tools returned by a
// prior client-executed search are merged into the toolset and the search
// interaction round-trips as tool_use / tool_result.
func TestConvertRequest_ToolSearchDiscoveredTools(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	messages := []provider.Message{
		provider.UserMessage("what's the weather?"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_1",
					Kind:      provider.ToolKindToolSearch,
					Name:      "tool_search",
					Execution: "client",
					Arguments: `{"query":"weather"}`,
				}),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{
					ID:        "call_1",
					Kind:      provider.ToolKindToolSearch,
					Execution: "client",
					Payload:   []byte(`[{"type":"function","name":"get_weather","description":"weather","parameters":{"type":"object"}}]`),
				}),
			},
		},
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{Kind: provider.ToolKindToolSearch, Execution: "client"},
		},
	}

	body := requestBody(t, completer, messages, options)

	tools := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected search + discovered tool, got %+v", tools)
	}
	if tools[1].(map[string]any)["name"] != "get_weather" {
		t.Fatalf("discovered tool missing: %+v", tools)
	}

	msgs := body["messages"].([]any)

	assistant := msgs[1].(map[string]any)
	blocks := assistant["content"].([]any)
	if blocks[0].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("search call not rendered as tool_use: %+v", blocks)
	}

	user := msgs[2].(map[string]any)
	blocks = user["content"].([]any)
	if blocks[0].(map[string]any)["type"] != "tool_result" {
		t.Fatalf("search output not rendered as tool_result: %+v", blocks)
	}
}
