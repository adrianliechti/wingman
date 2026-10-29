package hard_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/openai"
)

// Exercise real parallel tool rounds with signed reasoning. Replaying the
// streamed items (rather than reconstructing a transcript) catches ordering,
// signature, and terminal-snapshot discrepancies on the next provider request.
func TestParallelToolsReasoningStreamReplay(t *testing.T) {
	h := openai.New(t)
	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			tg := target{"wingman", h.Wingman, model.Name}
			input := userInput(hardtest.ParallelPrompt)
			calls := 0
			for round := 0; round < 6; round++ {
				events := postSSE(t, h, tg, map[string]any{
					"input": input, "tools": []any{weatherTool}, "store": false,
					"reasoning": map[string]any{"effort": "low", "summary": "auto"},
					"include":   []string{"reasoning.encrypted_content"}, "max_output_tokens": 2048,
				})
				var completed map[string]any
				var output []any
				active := ""
				closed := map[string]bool{}
				for _, event := range events {
					switch event.Data["type"] {
					case "response.output_item.added":
						item := event.Data["item"].(map[string]any)
						if active != "" {
							t.Fatalf("item %v started before %s completed", item["id"], active)
						}
						active = item["id"].(string)
					case "response.function_call_arguments.delta":
						id := event.Data["item_id"].(string)
						if closed[id] || active != id {
							t.Fatalf("arguments outside active call %s", id)
						}
					case "response.output_item.done":
						item := event.Data["item"].(map[string]any)
						id := item["id"].(string)
						if active != id || closed[id] {
							t.Fatalf("invalid item completion %s, active %s", id, active)
						}
						closed[id], active = true, ""
						output = append(output, item)
					case "response.completed":
						completed = event.Data["response"].(map[string]any)
					case "response.failed", "response.incomplete", "error":
						t.Fatalf("terminal failure: %v", event.Data)
					}
				}
				if completed == nil || active != "" {
					t.Fatal("stream did not complete all items")
				}
				if !reflect.DeepEqual(output, completed["output"]) {
					t.Fatal("streamed items differ from final output")
				}
				input = append(input, output...)
				var results []any
				for _, raw := range output {
					item := raw.(map[string]any)
					if item["type"] != "function_call" {
						continue
					}
					arguments := item["arguments"].(string)
					if !json.Valid([]byte(arguments)) {
						t.Fatalf("invalid final tool arguments: %s", arguments)
					}
					calls++
					results = append(results, map[string]any{"type": "function_call_output", "call_id": item["call_id"], "output": hardtest.ToolResult(item["name"].(string), arguments)})
				}
				if len(results) == 0 {
					if calls < 3 {
						t.Fatalf("only %d city lookups", calls)
					}
					hardtest.RequireMentionsCities(t, model.Name, messageText(completed))
					return
				}
				input = append(input, results...)
			}
			t.Fatal("tool loop did not finish")
		})
	}
}
