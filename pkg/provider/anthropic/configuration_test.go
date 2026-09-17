package anthropic

import (
	"encoding/json"
	"strings"
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

func TestPrefixBoundToolSearchIsRejected(t *testing.T) {
	c, _ := NewCompleter("http://localhost", "claude-fable-5-1")
	options := &provider.CompleteOptions{Tools: []provider.Tool{
		{Kind: provider.ToolKindToolSearch, Name: "tool_search_tool_regex", Execution: "server"},
		{Name: "lookup", Deferred: new(true), Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
	}}
	_, err := c.convertMessageRequest([]provider.Message{provider.UserMessage("Task")}, options)
	if err == nil || !strings.Contains(err.Error(), "prefix-preserving") {
		t.Fatalf("expected an explicit unsupported error: %v", err)
	}
}
