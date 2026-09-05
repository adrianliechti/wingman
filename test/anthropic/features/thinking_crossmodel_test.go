package features_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

// TestThinkingCrossModelToolUse switches models in the middle of a tool
// loop: turn 1 runs on one backend with thinking enabled, and its assistant
// content (thinking block with signature plus tool_use) is replayed verbatim
// to a different backend together with the tool result. The second backend
// cannot verify the first one's signature, so it must drop it instead of
// forwarding it upstream.
func TestThinkingCrossModelToolUse(t *testing.T) {
	h := anthropic.New(t)

	pairs := [][2]string{
		{"gpt-5.4", "claude-sonnet-4-6"},
		{"gemini-3.8-flash", "claude-sonnet-4-6"},
		{"claude-sonnet-4-6", "gpt-5.4"},
		{"claude-sonnet-4-6", "gemini-3.8-flash"},
	}

	for _, pair := range pairs {
		from, to := pair[0], pair[1]

		t.Run(from+"→"+to, func(t *testing.T) {
			h.SkipUnlessConfigured(t, from)
			h.SkipUnlessConfigured(t, to)

			turn1 := anthropic.WithModel(map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London? Use the tool."},
				},
				"tools":    []any{anthropic.WeatherTool},
				"thinking": map[string]any{"type": "adaptive"},
			}, from)

			resp1 := anthropic.PostMessages(t, h, h.Wingman, turn1)
			if resp1.StatusCode != 200 {
				t.Fatalf("turn 1 on %s returned %d: %s", from, resp1.StatusCode, string(resp1.RawBody))
			}

			content, _ := resp1.Body["content"].([]any)

			var toolUseID string
			var hasThinking bool
			for _, block := range content {
				obj, _ := block.(map[string]any)
				switch obj["type"] {
				case "tool_use":
					toolUseID, _ = obj["id"].(string)
				case "thinking":
					hasThinking = true
				}
			}

			if toolUseID == "" {
				t.Fatalf("turn 1 on %s produced no tool_use: %s", from, string(resp1.RawBody))
			}
			if !hasThinking {
				t.Logf("turn 1 on %s produced no thinking block; replay still exercises the switch", from)
			}

			turn2 := anthropic.WithModel(map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London? Use the tool."},
					{"role": "assistant", "content": content},
					{"role": "user", "content": []map[string]any{
						{"type": "tool_result", "tool_use_id": toolUseID, "content": "Sunny, 22°C"},
					}},
				},
				"tools":    []any{anthropic.WeatherTool},
				"thinking": map[string]any{"type": "adaptive"},
			}, to)

			resp2 := anthropic.PostMessages(t, h, h.Wingman, turn2)
			if resp2.StatusCode != 200 {
				t.Fatalf("turn 2 on %s returned %d: %s", to, resp2.StatusCode, string(resp2.RawBody))
			}

			anthropic.RequireTextContent(t, "wingman", resp2.Body)
		})
	}
}
