package responses_test

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
)

// TestReasoningRoundTripEncryptedOnlyHTTP exercises the round-trip path for
// reasoning items that arrive with only `encrypted_content` populated and no
// visible summary or content parts.
//
// This is the case OpenAI returns when no `reasoning.summary` is requested:
// the model still produces reasoning (chained via encrypted_content) but
// emits no human-visible parts. The intermediary must replay the item
// verbatim — any structural mutation (e.g. injecting a placeholder summary
// part) breaks OpenAI's verification of the encrypted blob with:
//
//	"the encrypted content for item rs_xxx could not be verified"
func TestReasoningRoundTripEncryptedOnlyHTTP(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Reasoning {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			turn1 := map[string]any{
				"input":   "Count the letter 'e' in 'nevertheless'.",
				"store":   false,
				"include": []string{"reasoning.encrypted_content"},
				"reasoning": map[string]any{
					"effort": "low",
					// summary intentionally omitted — we want reasoning items
					// with empty summary parts (only encrypted_content).
				},
			}

			wingmanResp1, err := h.Client.Post(ctx, h.Wingman, "/responses", withModel(turn1, model.Name))
			if err != nil {
				t.Fatalf("wingman turn 1 failed: %v", err)
			}
			if wingmanResp1.StatusCode != 200 {
				t.Fatalf("wingman turn 1 returned %d: %s", wingmanResp1.StatusCode, string(wingmanResp1.RawBody))
			}

			output := requireEncryptedOnlyReasoning(t, "wingman", wingmanResp1.Body)

			input := []any{
				map[string]any{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{"type": "input_text", "text": "Count the letter 'e' in 'nevertheless'."},
					},
				},
			}
			input = append(input, output...)
			input = append(input, map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Are you sure? Recount very carefully and tell me the final number."},
				},
			})

			turn2 := map[string]any{
				"input":   input,
				"store":   false,
				"include": []string{"reasoning.encrypted_content"},
				"reasoning": map[string]any{
					"effort": "low",
				},
			}

			wingmanResp2, err := h.Client.Post(ctx, h.Wingman, "/responses", withModel(turn2, model.Name))
			if err != nil {
				t.Fatalf("wingman turn 2 failed: %v", err)
			}
			if wingmanResp2.StatusCode != 200 {
				body := string(wingmanResp2.RawBody)
				if strings.Contains(body, "could not be verified") || strings.Contains(body, "could not be decrypted") {
					t.Fatalf("encrypted_content verification failed on round-trip — wingman is mutating reasoning items: %s", body)
				}
				t.Fatalf("wingman turn 2 returned %d: %s", wingmanResp2.StatusCode, body)
			}

			requireMessageOutput(t, "wingman", wingmanResp2.Body)
		})
	}
}

// requireEncryptedOnlyReasoning returns the response output and asserts that
// at least one reasoning item carries non-empty encrypted_content with an
// empty summary array — the exact shape that triggers the verification bug.
func requireEncryptedOnlyReasoning(t *testing.T, label string, body map[string]any) []any {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	foundEmpty := false
	for _, item := range output {
		obj, _ := item.(map[string]any)
		if obj["type"] != "reasoning" {
			continue
		}

		enc, _ := obj["encrypted_content"].(string)
		if enc == "" {
			continue
		}

		summary, _ := obj["summary"].([]any)
		if len(summary) == 0 {
			foundEmpty = true
		}
	}

	if !foundEmpty {
		t.Skipf("[%s] no reasoning item with empty summary found — model produced summary parts despite request, can't exercise the encrypted-only path", label)
	}

	return output
}
