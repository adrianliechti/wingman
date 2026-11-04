package features_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

// TestThinkingToolUseRoundTripHTTP exercises a real thinking + tool_use
// round trip. Anthropic enforces that any thinking_block returned alongside
// a tool_use must be replayed verbatim — signature included — when the
// tool_result follows. Synthetic prior turns can't satisfy that, so we
// issue turn 1 against each endpoint and replay the actual signed content.
func TestThinkingToolUseRoundTripHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			turn1 := map[string]any{
				"max_tokens": 16000,
				"thinking": map[string]any{
					"type": "adaptive",
				},
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London? Use the tool."},
				},
				"tools": []any{anthropic.WeatherTool},
			}

			anthropicResp1 := anthropic.PostMessages(t, h, h.Anthropic, anthropic.WithModel(turn1, h.ReferenceModel))
			if anthropicResp1.StatusCode != 200 {
				t.Fatalf("anthropic turn 1 returned %d: %s", anthropicResp1.StatusCode, string(anthropicResp1.RawBody))
			}
			wingmanResp1 := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(turn1, model.Name))
			if wingmanResp1.StatusCode != 200 {
				t.Fatalf("wingman turn 1 returned %d: %s", wingmanResp1.StatusCode, string(wingmanResp1.RawBody))
			}

			anthropicAssistant, anthropicToolID := extractAssistantContent(t, "anthropic", anthropicResp1.Body, "get_weather")
			wingmanAssistant, wingmanToolID := extractAssistantContent(t, "wingman", wingmanResp1.Body, "get_weather")

			turn2 := func(assistant []any, toolID string) map[string]any {
				return map[string]any{
					"max_tokens": 16000,
					"thinking": map[string]any{
						"type": "adaptive",
					},
					"messages": []map[string]any{
						{"role": "user", "content": "What's the weather in London? Use the tool."},
						{"role": "assistant", "content": assistant},
						{
							"role": "user",
							"content": []map[string]any{
								{
									"type":        "tool_result",
									"tool_use_id": toolID,
									"content":     "Sunny, 22°C with light winds",
								},
							},
						},
					},
					"tools": []any{anthropic.WeatherTool},
				}
			}

			anthropicResp2 := anthropic.PostMessages(t, h, h.Anthropic, anthropic.WithModel(turn2(anthropicAssistant, anthropicToolID), h.ReferenceModel))
			if anthropicResp2.StatusCode != 200 {
				t.Fatalf("anthropic turn 2 returned %d: %s", anthropicResp2.StatusCode, string(anthropicResp2.RawBody))
			}
			wingmanResp2 := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(turn2(wingmanAssistant, wingmanToolID), model.Name))
			if wingmanResp2.StatusCode != 200 {
				t.Fatalf("wingman turn 2 returned %d: %s", wingmanResp2.StatusCode, string(wingmanResp2.RawBody))
			}

			anthropic.RequireTextContent(t, "anthropic", anthropicResp2.Body)
			anthropic.RequireTextContent(t, "wingman", wingmanResp2.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			harness.CompareStructure(t, "response", anthropicResp2.Body, wingmanResp2.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// extractAssistantContent returns the raw content blocks from the first
// assistant turn (with thinking_block signatures intact) and the matching
// tool_use id, ready to be replayed verbatim in a follow-up.
func extractAssistantContent(t *testing.T, label string, body map[string]any, toolName string) ([]any, string) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	var toolID string
	for _, block := range content {
		obj, _ := block.(map[string]any)
		if obj["type"] != "tool_use" || obj["name"] != toolName {
			continue
		}
		id, _ := obj["id"].(string)
		toolID = id
		break
	}

	if toolID == "" {
		t.Fatalf("[%s] no tool_use block with name %q in turn 1", label, toolName)
	}

	return content, toolID
}
