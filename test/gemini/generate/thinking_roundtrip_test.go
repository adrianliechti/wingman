package generate_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

// TestThinkingRoundTripHTTP exercises a pure thinking continuation with
// no tool calls in between. Gemini 3 emits a thoughtSignature on at
// least one of the model's parts and rejects turn 2 with a 400 if the
// signature is dropped or mutated by the intermediary. Synthetic prior
// model turns can't satisfy that check, so we run turn 1 against each
// endpoint and replay the actual signed content verbatim.
//
// To make signature emission reliable we use a multi-step word problem
// (the model has to track state across operations) plus thinkingLevel
// "high" so the model engages the reasoning path. If upstream Gemini
// still doesn't emit a signature we skip — that's an upstream behavior
// change, not a Wingman bug. If upstream emits one but Wingman doesn't,
// we fail — that's exactly the round-trip bug this test is meant to
// catch.
func TestThinkingRoundTripHTTP(t *testing.T) {
	h := gemini.New(t)

	const turn1Prompt = "I have 3 apples and give away 1. Then I buy 5 more and give away 2. Then I eat 1. My friend gives me 3 and I give her back 2. How many apples do I have? Show your reasoning step by step."
	const turn2Prompt = "Are you sure? Recount very carefully, listing each step, and tell me the final number."

	for _, model := range gemini.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			turn1 := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": turn1Prompt}}},
				},
				"generationConfig": map[string]any{
					"thinkingConfig": map[string]any{
						"includeThoughts": true,
						"thinkingLevel":   "high",
					},
				},
			}

			geminiResp1 := postGemini(t, h, h.Gemini, h.ReferenceModel, turn1)
			if geminiResp1.StatusCode != 200 {
				t.Fatalf("gemini turn 1 returned %d: %s", geminiResp1.StatusCode, string(geminiResp1.RawBody))
			}
			wingmanResp1 := postGemini(t, h, h.Wingman, model.Name, turn1)
			if wingmanResp1.StatusCode != 200 {
				t.Fatalf("wingman turn 1 returned %d: %s", wingmanResp1.StatusCode, string(wingmanResp1.RawBody))
			}

			geminiParts := extractModelParts(t, "gemini", geminiResp1.Body)
			wingmanParts := extractModelParts(t, "wingman", wingmanResp1.Body)

			// If upstream itself didn't emit a signature there's nothing to
			// round-trip — skip rather than fail.
			if !hasSignedPart(geminiParts) {
				t.Skipf("gemini didn't emit a thoughtSignature in turn 1 — upstream model decided not to think; can't exercise round-trip")
			}

			// Upstream signed but wingman didn't: wingman is dropping the
			// signature on the way out. That IS the bug this test catches.
			if !hasSignedPart(wingmanParts) {
				t.Fatalf("gemini emitted a thoughtSignature but wingman did not — wingman is dropping signatures from the response")
			}

			turn2 := func(parts []any) map[string]any {
				return map[string]any{
					"contents": []map[string]any{
						{"role": "user", "parts": []map[string]any{{"text": turn1Prompt}}},
						{"role": "model", "parts": parts},
						{"role": "user", "parts": []map[string]any{{"text": turn2Prompt}}},
					},
					"generationConfig": map[string]any{
						"thinkingConfig": map[string]any{
							"includeThoughts": true,
							"thinkingLevel":   "high",
						},
					},
				}
			}

			geminiResp2 := postGemini(t, h, h.Gemini, h.ReferenceModel, turn2(geminiParts))
			if geminiResp2.StatusCode != 200 {
				t.Fatalf("gemini turn 2 returned %d: %s", geminiResp2.StatusCode, string(geminiResp2.RawBody))
			}
			wingmanResp2 := postGemini(t, h, h.Wingman, model.Name, turn2(wingmanParts))
			if wingmanResp2.StatusCode != 200 {
				t.Fatalf("wingman turn 2 returned %d: %s\n(turn 1 wingman parts: %v)",
					wingmanResp2.StatusCode, string(wingmanResp2.RawBody), wingmanParts)
			}

			requireTextResponse(t, "gemini", geminiResp2.Body)
			requireTextResponse(t, "wingman", wingmanResp2.Body)

			rules := gemini.DefaultResponseRules()
			harness.CompareStructure(t, "response", geminiResp2.Body, wingmanResp2.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// extractModelParts returns the first candidate's parts ready to be
// replayed in a follow-up turn under role "model". Signatures (if any)
// are preserved verbatim.
func extractModelParts(t *testing.T, label string, body map[string]any) []any {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("[%s] no candidates in turn 1", label)
	}

	cand, _ := candidates[0].(map[string]any)
	content, _ := cand["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	if len(parts) == 0 {
		t.Fatalf("[%s] candidate has no parts", label)
	}

	return parts
}

// hasSignedPart reports whether any part in the slice carries a
// non-empty thoughtSignature. Only signed parts trigger Gemini 3's
// verbatim-replay verification.
func hasSignedPart(parts []any) bool {
	for _, p := range parts {
		part, _ := p.(map[string]any)
		if sig, ok := part["thoughtSignature"].(string); ok && sig != "" {
			return true
		}
	}
	return false
}
