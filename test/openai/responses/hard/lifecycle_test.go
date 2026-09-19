package hard_test

import (
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

// Check each endpoint's own stream: independently generated provider IDs and
// text differ, but each stream must keep its IDs, ordering, and final items.
func TestStreamItemLifecycle(t *testing.T) {
	h := openai.New(t)
	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			for _, scenario := range []struct {
				name string
				body map[string]any
			}{
				{"text", map[string]any{"input": "Say hello and nothing else.", "store": false}},
				{"tool", map[string]any{"input": "Get the weather in Bern.", "tools": []any{weatherTool}, "tool_choice": "required", "store": false}},
				{"truncated", map[string]any{"input": "Write a long essay about the history of mathematics.", "max_output_tokens": 32, "store": false}},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					for _, tg := range targets(h, model) {
						t.Run(tg.label, func(t *testing.T) {
							requireStreamItemLifecycle(t, postSSE(t, h, tg, scenario.body))
						})
					}
				})
			}
		})
	}
}

func requireStreamItemLifecycle(t *testing.T, events []*harness.SSEEvent) {
	t.Helper()
	var responseID string
	var terminal map[string]any
	added, done := map[int]map[string]any{}, map[int]map[string]any{}
	ids := map[string]bool{}
	for _, event := range events {
		data := event.Data
		if response, ok := data["response"].(map[string]any); ok {
			id, _ := response["id"].(string)
			if id == "" {
				t.Fatal("response event has no ID")
			}
			if responseID != "" && responseID != id {
				t.Fatalf("response ID changed from %q to %q", responseID, id)
			}
			responseID = id
		}
		index, _ := data["output_index"].(float64)
		item, _ := data["item"].(map[string]any)
		switch data["type"] {
		case "response.output_item.added":
			id, _ := item["id"].(string)
			if id == "" || ids[id] || int(index) != len(added) {
				t.Fatalf("invalid item ID or output index: %v", data)
			}
			ids[id] = true
			added[int(index)] = item
		case "response.output_item.done":
			if done[int(index)] != nil || added[int(index)] == nil || added[int(index)]["id"] != item["id"] {
				t.Fatalf("item closed twice or changed ID: %v", data)
			}
			done[int(index)] = item
		case "response.completed", "response.incomplete":
			if terminal != nil {
				t.Fatal("multiple terminal responses")
			}
			terminal, _ = data["response"].(map[string]any)
		case "error", "response.failed":
			t.Fatalf("stream failed: %v", data)
		default:
			if id, _ := data["item_id"].(string); id != "" {
				if added[int(index)] == nil || added[int(index)]["id"] != id || done[int(index)] != nil {
					t.Fatalf("event addresses a missing, different, or closed item: %v", data)
				}
			}
		}
	}
	if terminal == nil {
		t.Fatal("missing terminal response")
	}
	output, _ := terminal["output"].([]any)
	if len(output) != len(added) || len(done) != len(added) {
		t.Fatalf("item counts changed: added=%d, done=%d, final=%d", len(added), len(done), len(output))
	}
	for i, item := range output {
		final, _ := item.(map[string]any)
		if !reflect.DeepEqual(withoutEncryptedContent(done[i]), withoutEncryptedContent(final)) {
			t.Errorf("item %d changed between output_item.done and terminal response: done=%v, final=%v", i, done[i], item)
		}
	}
}

// withoutEncryptedContent drops the opaque reasoning state: OpenAI re-encrypts
// it for the terminal response, so only the item's other fields must match.
func withoutEncryptedContent(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	copied := make(map[string]any, len(item))
	for key, value := range item {
		if key != "encrypted_content" {
			copied[key] = value
		}
	}
	return copied
}
