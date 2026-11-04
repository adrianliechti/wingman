package features_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

// Exercise the Claude Code path through every configured provider, including
// models whose native reasoning API is not Anthropic's thinking API.
func TestSignedThinkingReplayWithoutDisplay(t *testing.T) {
	h := anthropic.New(t)
	for _, model := range anthropic.DefaultModels() {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model.Name, stream), func(t *testing.T) {
				h.SkipUnlessConfigured(t, model.Name)
				history := []any{map[string]any{"role": "user", "content": "Think through this carefully: what is 17 times 23? Reply with the number."}}
				body := map[string]any{
					"model": model.Name, "max_tokens": 4096, "stream": stream,
					"thinking":      map[string]any{"type": "adaptive", "display": "omitted"},
					"output_config": map[string]any{"effort": "medium"}, "messages": history,
				}
				output := postLiveOutput(t, h.Client, h.Wingman, "messages", body)
				signed := false
				for _, raw := range output {
					block := raw.(map[string]any)
					if block["type"] != "thinking" {
						continue
					}
					if signature, _ := block["signature"].(string); signature != "" {
						signed = true
					}
					if strings.HasPrefix(model.Name, "gpt-") && block["thinking"] != "" {
						t.Fatal("OpenAI returned summary text despite display=omitted")
					}
				}
				if !signed {
					t.Fatal("no signed thinking to replay")
				}
				body["messages"] = append(history, map[string]any{"role": "assistant", "content": output}, map[string]any{"role": "user", "content": "Verify that multiplication and repeat the number."})
				answer := postLiveOutput(t, h.Client, h.Wingman, "messages", body)
				var text strings.Builder
				for _, raw := range answer {
					block := raw.(map[string]any)
					if block["type"] == "text" {
						text.WriteString(block["text"].(string))
					}
				}
				if !strings.Contains(text.String(), "391") {
					t.Fatalf("replay lost the answer: %q", text.String())
				}
			})
		}
	}
}

// TestThinkingRoundTripHTTP exercises a pure thinking round-trip with no
// tool calls in between. Anthropic enforces that any thinking_block returned
// from a previous turn must be replayed verbatim — signature included — when
// thinking is enabled on the next turn. If the intermediary drops thinking
// blocks on input parsing, the second turn fails or silently degrades.
//
// Synthetic prior turns can't satisfy the signature check, so we issue
// turn 1 against each endpoint and replay the actual signed content.
func TestThinkingRoundTripHTTP(t *testing.T) {
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
					"type":          "enabled",
					"budget_tokens": 5000,
				},
				"output_config": map[string]any{"effort": "high"},
				"messages": []map[string]any{
					{"role": "user", "content": harness.ReasoningPrompt},
				},
			}

			anthropicResp1 := anthropic.PostMessages(t, h, h.Anthropic, anthropic.WithModel(turn1, h.ReferenceModel))
			if anthropicResp1.StatusCode != 200 {
				t.Fatalf("anthropic turn 1 returned %d: %s", anthropicResp1.StatusCode, string(anthropicResp1.RawBody))
			}
			wingmanResp1 := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(turn1, model.Name))
			if wingmanResp1.StatusCode != 200 {
				t.Fatalf("wingman turn 1 returned %d: %s", wingmanResp1.StatusCode, string(wingmanResp1.RawBody))
			}

			anthropicAssistant := requireSignedThinkingContent(t, "anthropic", anthropicResp1.Body)
			wingmanAssistant := requireSignedThinkingContent(t, "wingman", wingmanResp1.Body)

			turn2 := func(assistant []any) map[string]any {
				return map[string]any{
					"max_tokens": 16000,
					"thinking": map[string]any{
						"type":          "enabled",
						"budget_tokens": 5000,
					},
					"output_config": map[string]any{"effort": "high"},
					"messages": []map[string]any{
						{"role": "user", "content": harness.ReasoningPrompt},
						{"role": "assistant", "content": assistant},
						{"role": "user", "content": "Verify your answer against all three congruences and repeat the integer."},
					},
				}
			}

			anthropicResp2 := anthropic.PostMessages(t, h, h.Anthropic, anthropic.WithModel(turn2(anthropicAssistant), h.ReferenceModel))
			if anthropicResp2.StatusCode != 200 {
				t.Fatalf("anthropic turn 2 returned %d: %s", anthropicResp2.StatusCode, string(anthropicResp2.RawBody))
			}
			wingmanResp2 := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(turn2(wingmanAssistant), model.Name))
			if wingmanResp2.StatusCode != 200 {
				t.Fatalf("wingman turn 2 returned %d: %s\n(turn 1 wingman content: %v)",
					wingmanResp2.StatusCode, string(wingmanResp2.RawBody), wingmanAssistant)
			}

			anthropic.RequireTextContent(t, "anthropic", anthropicResp2.Body)
			anthropic.RequireTextContent(t, "wingman", wingmanResp2.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content.*.thinking"] = harness.FieldIgnore
			rules["content.*.signature"] = harness.FieldNonEmpty
			harness.CompareStructure(t, "response", anthropicResp2.Body, wingmanResp2.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// requireSignedThinkingContent returns the assistant content blocks and
// asserts that at least one thinking block carries a non-empty signature.
// That signature is what the next turn must replay verbatim.
func requireSignedThinkingContent(t *testing.T, label string, body map[string]any) []any {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	for _, block := range content {
		obj, _ := block.(map[string]any)
		if obj["type"] != "thinking" {
			continue
		}
		if sig, _ := obj["signature"].(string); sig != "" {
			return content
		}
	}

	t.Fatalf("[%s] no signed thinking block in turn 1 — cannot exercise round-trip", label)
	return nil
}
