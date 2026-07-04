package features_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

// TestUsageTokensCorrectness exercises usageMetadata accounting on
// /v1beta/models/{model}:generateContent for every configured model (Gemini
// plus cross-provider models served through the Gemini surface).
//
// Token counts are never compared for equality against the reference Gemini
// account — tokenizers and prompt framing differ per provider. We assert
// self-consistent invariants on each wingman response (the Gemini wire
// convention: promptTokenCount is cache-inclusive, candidatesTokenCount
// excludes thoughts, totalTokenCount == prompt + candidates + thoughts) and
// verify the same *tendency* (a longer prompt costs more prompt tokens) as the
// reference.
func TestUsageTokensCorrectness(t *testing.T) {
	h := gemini.New(t)

	shortBody := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "Reply with the single word: OK."}}},
		},
	}
	longBody := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": buildLongGeminiPrompt("gemini-usage") + "\n\nReply with the single word: OK."}}},
		},
	}

	reference := referenceUsage(shortBody, longBody, func(t *testing.T, body map[string]any) geminiUsageResult {
		return geminiUsage(t, h, h.Gemini, h.ReferenceModel, body)
	})

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := geminiUsage(t, h, h.Wingman, model.Name, shortBody)
			long := geminiUsage(t, h, h.Wingman, model.Name, longBody)

			short.assertInvariants(t, "short")
			long.assertInvariants(t, "long")

			if long.prompt <= short.prompt {
				t.Errorf("expected long prompt promptTokenCount (%d) > short prompt promptTokenCount (%d)\nshort=%+v long=%+v",
					long.prompt, short.prompt, short, long)
			}

			// Tendency must match the reference account's direction. We compare
			// the *delta sign*, never the magnitudes.
			refShort, refLong := reference(t)

			if (long.prompt > short.prompt) != (refLong.prompt > refShort.prompt) {
				t.Errorf("prompt-token tendency disagrees with reference: "+
					"wingman short/long=%d/%d, reference short/long=%d/%d",
					short.prompt, long.prompt, refShort.prompt, refLong.prompt)
			}
		})
	}
}

func TestUsageTokensCorrectnessSSE(t *testing.T) {
	h := gemini.New(t)

	shortBody := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "Reply with the single word: OK."}}},
		},
	}
	longBody := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": buildLongGeminiPrompt("gemini-usage-sse") + "\n\nReply with the single word: OK."}}},
		},
	}

	reference := referenceUsage(shortBody, longBody, func(t *testing.T, body map[string]any) geminiUsageResult {
		return geminiUsageSSE(t, h, h.Gemini, h.ReferenceModel, body)
	})

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := geminiUsageSSE(t, h, h.Wingman, model.Name, shortBody)
			long := geminiUsageSSE(t, h, h.Wingman, model.Name, longBody)

			short.assertInvariants(t, "short stream")
			long.assertInvariants(t, "long stream")

			if long.prompt <= short.prompt {
				t.Errorf("expected long prompt streaming promptTokenCount (%d) > short prompt streaming promptTokenCount (%d)\nshort=%+v long=%+v",
					long.prompt, short.prompt, short, long)
			}

			refShort, refLong := reference(t)

			if (long.prompt > short.prompt) != (refLong.prompt > refShort.prompt) {
				t.Errorf("streaming prompt-token tendency disagrees with reference: "+
					"wingman short/long=%d/%d, reference short/long=%d/%d",
					short.prompt, long.prompt, refShort.prompt, refLong.prompt)
			}
		})
	}
}

// referenceUsage returns a lazy, memoized fetch of the reference account's
// short/long usage. The reference does not depend on the model under test, so
// it is requested at most once per test (subtests run sequentially) and only
// when at least one model is actually configured. Only the prompt count and
// tendency are asserted — reference responses are not held to wingman's wire
// invariants (e.g. implicit-caching details differ per account).
func referenceUsage(shortBody, longBody map[string]any, fetch func(t *testing.T, body map[string]any) geminiUsageResult) func(t *testing.T) (geminiUsageResult, geminiUsageResult) {
	var cached *[2]geminiUsageResult

	return func(t *testing.T) (geminiUsageResult, geminiUsageResult) {
		t.Helper()

		if cached == nil {
			short := fetch(t, shortBody)
			long := fetch(t, longBody)

			if short.prompt <= 0 || long.prompt <= 0 {
				t.Fatalf("reference promptTokenCount not positive: short=%d long=%d", short.prompt, long.prompt)
			}

			if long.prompt <= short.prompt {
				t.Fatalf("reference did not show expected tendency: long prompt %d <= short prompt %d",
					long.prompt, short.prompt)
			}

			cached = &[2]geminiUsageResult{short, long}
		}

		return cached[0], cached[1]
	}
}

// TestUsageTokensThinking verifies the reasoning-token split end-to-end: with
// thinking enabled, thoughtsTokenCount is reported separately and the visible
// candidatesTokenCount excludes it (a regression guard for the wire mapping
// candidatesTokenCount = OutputTokens - ReasoningTokens). The strict total
// identity is asserted for every thinking-capable model, and thoughts > 0 is
// required cross-provider (claude's thinking_tokens and gpt's reasoning_tokens
// both surface here as thoughtsTokenCount). Bedrock models are exempt — the
// AWS Converse API reports no reasoning bucket in its usage, so the spend
// stays folded into candidatesTokenCount there.
func TestUsageTokensThinking(t *testing.T) {
	h := gemini.New(t)

	body := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "I have 3 apples and give away 1. Then I buy 5 more and give away 2. Then I eat 1. My friend gives me 3 and I give her back 2. How many apples do I have? Show your reasoning step by step."}}},
		},
		"generationConfig": map[string]any{
			"thinkingConfig": map[string]any{
				"includeThoughts": true,
			},
		},
	}

	for _, model := range gemini.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			u := geminiUsage(t, h, h.Wingman, model.Name, body)
			u.assertInvariants(t, "thinking")

			bedrock := strings.Contains(strings.ToLower(model.Name), "bedrock")
			if !bedrock && u.thoughts <= 0 {
				t.Errorf("expected thoughtsTokenCount > 0 with thinking enabled, got %d (%+v)", u.thoughts, u)
			}

			// Whenever thoughts are spent, candidatesTokenCount must exclude
			// them, so the cache-inclusive total exceeds the visible candidates.
			if u.thoughts > 0 && u.total <= u.candidates {
				t.Errorf("expected totalTokenCount (%d) > candidatesTokenCount (%d) when thoughtsTokenCount=%d",
					u.total, u.candidates, u.thoughts)
			}
		})
	}
}

type geminiUsageResult struct {
	prompt     int
	candidates int
	thoughts   int
	cached     int
	total      int
}

func (u geminiUsageResult) assertInvariants(t *testing.T, label string) {
	t.Helper()

	if u.prompt <= 0 {
		t.Errorf("[%s] expected promptTokenCount > 0, got %d", label, u.prompt)
	}
	if u.candidates <= 0 {
		t.Errorf("[%s] expected candidatesTokenCount > 0, got %d", label, u.candidates)
	}
	if u.thoughts < 0 {
		t.Errorf("[%s] negative thoughtsTokenCount: %d", label, u.thoughts)
	}
	// cachedContentTokenCount is a subset of promptTokenCount (Gemini reports a
	// cache-inclusive prompt count).
	if u.cached < 0 || u.cached > u.prompt {
		t.Errorf("[%s] cachedContentTokenCount (%d) must be within [0, promptTokenCount=%d]", label, u.cached, u.prompt)
	}
	// Gemini wire convention: totalTokenCount == prompt + candidates + thoughts.
	if u.total != u.prompt+u.candidates+u.thoughts {
		t.Errorf("[%s] totalTokenCount (%d) != promptTokenCount (%d) + candidatesTokenCount (%d) + thoughtsTokenCount (%d)",
			label, u.total, u.prompt, u.candidates, u.thoughts)
	}
}

func geminiUsage(t *testing.T, h *gemini.Harness, ep harness.Endpoint, model string, body map[string]any) geminiUsageResult {
	t.Helper()

	resp := gemini.PostGemini(t, h, ep, model, gemini.WithModel(body, model))
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", ep.Name, resp.StatusCode, string(resp.RawBody))
	}

	return geminiUsageFromMap(t, resp.Body["usageMetadata"])
}

func geminiUsageSSE(t *testing.T, h *gemini.Harness, ep harness.Endpoint, model string, body map[string]any) geminiUsageResult {
	t.Helper()

	events := gemini.PostGeminiSSE(t, h, ep, model, gemini.WithModel(body, model))

	// usageMetadata is emitted on every chunk and accumulates; the final usage
	// is carried by the last event that reports a positive totalTokenCount.
	var last geminiUsageResult
	var found bool
	for _, event := range events {
		usage, ok := event.Data["usageMetadata"]
		if !ok {
			continue
		}
		u := geminiUsageFromMap(t, usage)
		if u.total > 0 {
			last = u
			found = true
		}
	}

	if !found {
		t.Fatalf("[%s] no usageMetadata with totalTokenCount > 0 found in %d events", ep.Name, len(events))
	}
	return last
}

func geminiUsageFromMap(t *testing.T, usage any) geminiUsageResult {
	t.Helper()

	obj, ok := usage.(map[string]any)
	if !ok {
		t.Fatalf("usageMetadata is %T, want object", usage)
	}

	return geminiUsageResult{
		prompt:     geminiInt(obj, "promptTokenCount"),
		candidates: geminiInt(obj, "candidatesTokenCount"),
		thoughts:   geminiInt(obj, "thoughtsTokenCount"),
		cached:     geminiInt(obj, "cachedContentTokenCount"),
		total:      geminiInt(obj, "totalTokenCount"),
	}
}

func geminiInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func buildLongGeminiPrompt(seed string) string {
	var b strings.Builder
	b.WriteString("Reference material for scenario ")
	b.WriteString(seed)
	b.WriteString(":\n\n")
	for i := range 80 {
		b.WriteString("This sentence exists purely to add tokens to the prompt so that the input token count is meaningfully larger than a trivial request. ")
		if i%8 == 7 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
