package toolsearch

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestResolveReferencesPreservesLoadedDefinitions(t *testing.T) {
	for _, payload := range []string{
		`[{"type":"function","name":"lookup","parameters":{"type":"object"},"defer_loading":true,"strict":true,"description":"status"}]`,
		`[{"type":"namespace","name":"project","description":"project tools","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"defer_loading":true}]}]`,
	} {
		messages := []provider.Message{{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolResultContent(provider.ToolResult{Kind: provider.ToolKindToolSearch, Payload: []byte(payload)})}}}
		resolved, err := ResolveResults(messages, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(resolved[0].Content[0].ToolResult.Payload); got != payload {
			t.Fatalf("rewrote loaded definitions: %s", got)
		}
	}
	result := provider.ToolResult{Kind: provider.ToolKindToolSearch, Payload: []byte(`[{"type":"function","name":"lookup"}]`)}
	input := []provider.Message{{Content: []provider.Content{provider.ToolResultContent(result)}}}
	resolved, err := ResolveResults(input, []provider.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}
	var loaded []map[string]any
	json.Unmarshal(resolved[0].Content[0].ToolResult.Payload, &loaded)
	if loaded[0]["parameters"] == nil || loaded[0]["defer_loading"] != true {
		t.Fatalf("unusable loaded tool: %v", loaded)
	}
	if string(input[0].Content[0].ToolResult.Payload) != string(result.Payload) {
		t.Fatal("mutated source references")
	}
}

func TestInlineSearchHistory(t *testing.T) {
	for _, execution := range []string{"server", "client"} {
		t.Run(execution, func(t *testing.T) {
			deferred := true
			options := &provider.CompleteOptions{Tools: []provider.Tool{
				{Kind: provider.ToolKindToolSearch, Execution: execution},
				{Name: "lookup", Parameters: map[string]any{"type": "object"}, Deferred: &deferred},
			}}
			messages := []provider.Message{
				provider.UserMessage("look up status"),
				{Role: provider.MessageRoleAssistant, Content: []provider.Content{
					provider.ToolCallContent(provider.ToolCall{ID: "search", Kind: provider.ToolKindToolSearch, Execution: execution, Arguments: `{"query":"lookup"}`}),
					provider.ToolResultContent(provider.ToolResult{ID: "search", Kind: provider.ToolKindToolSearch, Execution: execution, Payload: []byte(`[{"type":"function","name":"lookup"}]`)}),
					provider.ToolCallContent(provider.ToolCall{ID: "call", Name: "lookup", Arguments: `{}`}),
				}},
				provider.ToolMessage("call", "READY"),
			}
			before, _ := json.Marshal(messages)
			resolved, updated := Inline(messages, options)
			if execution == "server" {
				if !reflect.DeepEqual(updated.Tools, options.Tools[1:]) || len(resolved[1].Content) != 1 || resolved[1].Content[0].ToolCall.ID != "call" {
					t.Fatal("hosted fallback lost the catalog or executable call")
				}
			} else if updated.Tools[0].Kind != provider.ToolKindFunction || updated.Tools[0].Name != Name || resolved[1].Content[1].ToolResult.Parts[0].Text == "" {
				t.Fatal("client fallback lost its search function or output")
			}
			after, _ := json.Marshal(messages)
			if string(before) != string(after) || options.Tools[0].Kind != provider.ToolKindToolSearch {
				t.Fatal("mutated original search history or tools")
			}
		})
	}
}
