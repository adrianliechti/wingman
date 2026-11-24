package hard_test

import (
	"encoding/json/jsontext"
	"maps"
	"slices"
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
			h.RequireConfigured(t, model.Name)
			tg := target{"wingman", h.Wingman, model.Name}
			input := userInput(hardtest.ParallelPrompt)
			calls := 0
			for range 6 {
				events := postSSE(t, h, tg, map[string]any{
					"input": input, "tools": []any{weatherTool}, "store": false,
					"reasoning": map[string]any{"effort": "low", "summary": "auto"},
					"include":   []string{"reasoning.encrypted_content"}, "max_output_tokens": 2048,
				})
				requireStreamItemLifecycle(t, events)
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
				input = append(input, output...)
				var results []any
				for _, raw := range output {
					item := raw.(map[string]any)
					if item["type"] != "function_call" {
						continue
					}
					arguments := item["arguments"].(string)
					if !jsontext.Value([]byte(arguments)).IsValid() {
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

// A stateless client can collect item.done events as they arrive or use the
// terminal output array. Both transcripts must survive the next tool turn.
// A plain request sets neither reasoning options nor the legacy include, as
// new clients may: it still receives the ciphertext, but no reasoning text.
func TestStatelessReasoningStreamReplay(t *testing.T) {
	h := openai.New(t)
	testTarget := func(t *testing.T, tg target, reasoning map[string]any) {
		input := userInput("Find the smallest positive integer x such that x mod 7 = 3, x mod 11 = 5 and x mod 13 = 7. The city is determined by x mod 3: 0 means Tokyo, 1 means Bern, and 2 means Paris. Calculate x first, then call get_weather exactly once for the resulting city. Report x and the weather.")
		request := map[string]any{
			"input": input, "tools": []any{weatherTool}, "store": false,
			// An explicit null keeps the harness from disabling reasoning.
			"reasoning": reasoning,
		}
		round := runFlowRound(t, h, tg, request, true)
		if round.response["status"] != "completed" {
			t.Fatalf("first turn status = %v", round.response["status"])
		}
		var done []any
		for _, event := range round.events {
			if sseType(event) == "response.output_item.done" {
				done = append(done, event.Data["item"])
			}
		}
		for _, replay := range []struct {
			name   string
			output []any
		}{
			{"item_done", done}, {"terminal_output", round.output()},
		} {
			t.Run(replay.name, func(t *testing.T) {
				requireToolRound(t, tg.label, replay.output)
				// The reference model can skip reasoning at its default effort;
				// the thinking models under test reason through this puzzle.
				// A stateless reasoning item is replayable only with ciphertext.
				items := itemsOfType(replay.output, "reasoning")
				if (reasoning != nil || tg.label == "wingman") && len(items) == 0 {
					t.Fatal("tool turn lacked reasoning to replay")
				}
				for _, item := range items {
					summary, _ := item["summary"].([]any)
					content, _ := item["content"].([]any)
					if ciphertext, _ := item["encrypted_content"].(string); ciphertext == "" {
						t.Fatalf("reasoning item has no encrypted_content: %v", item)
					}
					if reasoning == nil && (len(summary) != 0 || len(content) != 0) {
						t.Fatalf("plain request exposed reasoning text: %v", item)
					}
				}
				next := maps.Clone(request)
				history := append(slices.Clone(input), replay.output...)
				next["input"] = append(history, toolOutputs(replay.output)...)
				answer := post(t, h, tg, next)
				if answer["status"] != "completed" {
					t.Fatalf("replay status = %v", answer["status"])
				}
				requireAnswerRound(t, tg.label, answer)
			})
		}
	}
	referenceTested := false
	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}
		t.Run(model.Name, func(t *testing.T) {
			h.RequireConfigured(t, model.Name)
			for _, tg := range targets(h, model) {
				if tg.label == "openai" {
					if referenceTested {
						continue
					}
					referenceTested = true
				}
				t.Run(tg.label, func(t *testing.T) {
					t.Run("summary", func(t *testing.T) { testTarget(t, tg, map[string]any{"effort": "high", "summary": "auto"}) })
					t.Run("plain", func(t *testing.T) { testTarget(t, tg, nil) })
				})
			}
		})
	}
}
