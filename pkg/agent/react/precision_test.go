package react

import (
	"encoding/json/v2"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestToolExecutionPreservesNumbers(t *testing.T) {
	const args = `{"id":9007199254740993,"values":[1.234567890123456789,1e400]}`
	model := &mockCompleter{responses: [][]provider.Completion{
		{{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "c1", Name: "lookup", Arguments: args})}}}},
		{{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("done")}}}},
	}}
	tools := &mockToolProvider{tools: []provider.Tool{{Name: "lookup"}}}
	chain, err := New("test", WithCompleter(model), WithTools(tools))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collectCompletions(chain.Complete(t.Context(), []provider.Message{provider.UserMessage("lookup")}, nil)); err != nil {
		t.Fatal(err)
	}
	if len(tools.executeCalls) != 1 {
		t.Fatalf("executions: %v", tools.executeCalls)
	}
	data, err := json.Marshal(tools.executeCalls[0].Params, json.Deterministic(true))
	if err != nil || string(data) != args {
		t.Fatalf("arguments changed: %s (%v)", data, err)
	}
}
