package features_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// TestReasoningRoundTripHTTP exercises the encrypted_content round-trip
// path. With store=false plus include=["reasoning.encrypted_content"], the
// model returns reasoning items that must be replayed verbatim on the next
// turn so the model can chain its own reasoning. Synthetic prior turns
// can't satisfy that, so we issue turn 1 against each endpoint and replay
// the real reasoning items (encrypted_content intact).
func TestReasoningRoundTripHTTP(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.RequireConfigured(t, model.Name)
			turn1 := map[string]any{
				"input":   harness.ReasoningPrompt,
				"store":   false,
				"include": []string{"reasoning.encrypted_content"},
				"reasoning": map[string]any{
					"effort":  "high",
					"summary": "auto",
				},
			}

			openaiResp1, err := h.Client.Post(ctx, h.OpenAI, "/responses", responses.WithModel(turn1, h.ReferenceModel))
			if err != nil {
				t.Fatalf("openai turn 1 failed: %v", err)
			}
			if openaiResp1.StatusCode != 200 {
				t.Fatalf("openai turn 1 returned %d: %s", openaiResp1.StatusCode, string(openaiResp1.RawBody))
			}

			wingmanResp1, err := h.Client.Post(ctx, h.Wingman, "/responses", responses.WithModel(turn1, model.Name))
			if err != nil {
				t.Fatalf("wingman turn 1 failed: %v", err)
			}
			if wingmanResp1.StatusCode != 200 {
				t.Fatalf("wingman turn 1 returned %d: %s", wingmanResp1.StatusCode, string(wingmanResp1.RawBody))
			}

			openaiOutput := requireEncryptedReasoning(t, "openai", openaiResp1.Body)
			wingmanOutput := requireEncryptedReasoning(t, "wingman", wingmanResp1.Body)

			turn2 := func(prevOutput []any) map[string]any {
				input := []any{
					map[string]any{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": harness.ReasoningPrompt},
						},
					},
				}
				input = append(input, prevOutput...)
				input = append(input, map[string]any{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{"type": "input_text", "text": "Are you sure? Verify each remainder and the smallest positive solution with a different method."},
					},
				})

				return map[string]any{
					"input":   input,
					"store":   false,
					"include": []string{"reasoning.encrypted_content"},
					"reasoning": map[string]any{
						"effort":  "high",
						"summary": "auto",
					},
				}
			}

			openaiResp2, err := h.Client.Post(ctx, h.OpenAI, "/responses", responses.WithModel(turn2(openaiOutput), h.ReferenceModel))
			if err != nil {
				t.Fatalf("openai turn 2 failed: %v", err)
			}
			if openaiResp2.StatusCode != 200 {
				t.Fatalf("openai turn 2 returned %d: %s", openaiResp2.StatusCode, string(openaiResp2.RawBody))
			}

			wingmanResp2, err := h.Client.Post(ctx, h.Wingman, "/responses", responses.WithModel(turn2(wingmanOutput), model.Name))
			if err != nil {
				t.Fatalf("wingman turn 2 failed: %v", err)
			}
			if wingmanResp2.StatusCode != 200 {
				t.Fatalf("wingman turn 2 returned %d: %s", wingmanResp2.StatusCode, string(wingmanResp2.RawBody))
			}

			responses.RequireMessageOutput(t, "openai", openaiResp2.Body)
			responses.RequireMessageOutput(t, "wingman", wingmanResp2.Body)

			rules := openai.DefaultResponsesResponseRules()
			// Different reasoning backends emit a different number of output
			// items (e.g. Claude's reasoning maps to a single message, while
			// OpenAI emits a separate reasoning item). The e2e check above
			// already validates that turn 2 produced a coherent message.
			rules["output"] = harness.FieldPresence
			rules["moderation"] = harness.FieldIgnore
			rules["prompt_cache_retention"] = harness.FieldIgnore
			harness.CompareStructure(t, "response", openaiResp2.Body, wingmanResp2.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// requireEncryptedReasoning returns the response output and asserts that
// at least one reasoning item carries non-empty encrypted_content.
func requireEncryptedReasoning(t *testing.T, label string, body map[string]any) []any {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, _ := item.(map[string]any)
		if obj["type"] != "reasoning" {
			continue
		}
		if enc, _ := obj["encrypted_content"].(string); enc != "" {
			return output
		}
	}

	t.Fatalf("[%s] no reasoning item with encrypted_content found", label)
	return nil
}
