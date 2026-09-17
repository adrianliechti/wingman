package anthropic

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestPerMessageEffortPreservesPosition(t *testing.T) {
	c, _ := NewCompleter("http://localhost", "claude-opus-5")
	input := []provider.Message{
		provider.SystemMessage("Stable instructions"),
		provider.UserMessage("Plan"),
		provider.AssistantMessage("Plan ready"),
		{Content: []provider.Content{provider.ConfigurationUpdateContent(provider.ConfigurationUpdate{ReasoningEffort: provider.EffortLow})}},
		provider.UserMessage("Summarize"),
	}
	request, err := c.convertMessageRequest(input, &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Effort: provider.EffortHigh}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	messages := body["messages"].([]any)
	update := messages[2].(map[string]any)
	if len(messages) != 4 || update["role"] != "system" || len(update["content"].([]any)) != 0 || update["output_config"].(map[string]any)["effort"] != "low" {
		t.Fatalf("lost effort position: %s", data)
	}
	if body["output_config"].(map[string]any)["effort"] != "high" {
		t.Fatal("changed prefix effort")
	}
	if len(request.Betas) != 1 || request.Betas[0] != "mid-conversation-output-config-2026-07-01" {
		t.Fatalf("missing beta: %v", request.Betas)
	}
	fallback, _ := NewCompleter("http://localhost", "claude-sonnet-4-6")
	body = requestBody(t, fallback, input, nil)
	if body["output_config"].(map[string]any)["effort"] != "low" || len(body["messages"].([]any)) != 3 {
		t.Fatalf("lost effort on a model without positional updates: %v", body)
	}
}

func TestToolSearchPreservesDefinitionsAndResults(t *testing.T) {
	c, _ := NewCompleter("http://localhost", "claude-fable-5-1")
	options := &provider.CompleteOptions{Tools: []provider.Tool{
		{Kind: provider.ToolKindToolSearch, Name: "tool_search_tool_regex", Execution: "server"},
		{Name: "lookup", Deferred: new(true), Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
	}}
	history := []provider.Message{provider.UserMessage("Task")}
	before := requestBody(t, c, history, options)
	history = append(history, provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.ToolCallContent(provider.ToolCall{ID: "srv_1", Name: "tool_search_tool_regex", Kind: provider.ToolKindToolSearch, Execution: "server", Arguments: `{"pattern":"lookup"}`}),
		provider.ToolResultContent(provider.ToolResult{ID: "srv_1", Kind: provider.ToolKindToolSearch, Execution: "server", Payload: []byte(`[{"type":"function","name":"lookup"}]`)}),
		provider.ReasoningContent(provider.Reasoning{Signature: "signed-state"}),
		provider.ToolCallContent(provider.ToolCall{ID: "tool_1", Name: "lookup", Arguments: "{}"}),
	}}, provider.ToolMessage("tool_1", "Result"))
	after := requestBody(t, c, history, options)
	if !reflect.DeepEqual(before["tools"], after["tools"]) {
		t.Fatal("tool search changed the signed prefix")
	}
	blocks := after["messages"].([]any)[1].(map[string]any)["content"].([]any)
	for i, typ := range []string{"server_tool_use", "tool_search_tool_result", "thinking", "tool_use"} {
		if blocks[i].(map[string]any)["type"] != typ {
			t.Fatalf("lost hosted history: %v", blocks)
		}
	}
}
