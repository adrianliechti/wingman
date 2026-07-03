package features_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

// TestUsageTokensCorrectness exercises the usage accounting reported on
// /v1/messages for every configured model (Claude, Bedrock, and cross-provider
// models served through the Anthropic surface).
//
// Token counts are never compared for equality against the reference account —
// tokenizers and prompt framing differ per provider, and wingman auto-applies
// prompt caching (so the prompt lands in cache_creation_input_tokens rather than
// input_tokens). The cache-inclusive total (input + cache_read + cache_creation)
// is the provider-neutral quantity; we assert self-consistent invariants and the
// same *tendency* as the reference (a longer prompt costs more total tokens).
func TestUsageTokensCorrectness(t *testing.T) {
	h := anthropic.New(t)

	shortBody := map[string]any{
		"max_tokens": 16,
		"messages": []map[string]any{
			{"role": "user", "content": "Reply with the single word: OK."},
		},
	}
	longBody := map[string]any{
		"max_tokens": 16,
		"messages": []map[string]any{
			{"role": "user", "content": buildLongUserPrompt("usage-tendency") + "\n\nReply with the single word: OK."},
		},
	}

	reference := referenceUsage(shortBody, longBody, func(t *testing.T, body map[string]any) messagesUsageResult {
		return messagesUsage(t, h, h.Anthropic, h.ReferenceModel, body)
	})

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := messagesUsage(t, h, h.Wingman, model.Name, shortBody)
			long := messagesUsage(t, h, h.Wingman, model.Name, longBody)

			short.assertInvariants(t, "short")
			long.assertInvariants(t, "long")

			// Tendency within wingman: a longer prompt costs more total input
			// tokens (counting cached + fresh, since wingman may cache the prompt).
			if long.totalInput() <= short.totalInput() {
				t.Errorf("expected long prompt total input (%d) > short prompt total input (%d)\nshort=%+v long=%+v",
					long.totalInput(), short.totalInput(), short, long)
			}

			// Tendency must match the reference account's direction. We compare
			// the *delta sign*, never the magnitudes.
			refShort, refLong := reference(t)

			if (long.totalInput() > short.totalInput()) != (refLong.totalInput() > refShort.totalInput()) {
				t.Errorf("total-input tendency disagrees with reference: "+
					"wingman short/long=%d/%d, reference short/long=%d/%d",
					short.totalInput(), long.totalInput(), refShort.totalInput(), refLong.totalInput())
			}
		})
	}
}

func TestUsageTokensCorrectnessSSE(t *testing.T) {
	h := anthropic.New(t)

	shortBody := map[string]any{
		"max_tokens": 16,
		"messages": []map[string]any{
			{"role": "user", "content": "Reply with the single word: OK."},
		},
	}
	longBody := map[string]any{
		"max_tokens": 16,
		"messages": []map[string]any{
			{"role": "user", "content": buildLongUserPrompt("usage-tendency-sse") + "\n\nReply with the single word: OK."},
		},
	}

	reference := referenceUsage(shortBody, longBody, func(t *testing.T, body map[string]any) messagesUsageResult {
		return messagesUsageSSE(t, h, h.Anthropic, h.ReferenceModel, body)
	})

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := messagesUsageSSE(t, h, h.Wingman, model.Name, shortBody)
			long := messagesUsageSSE(t, h, h.Wingman, model.Name, longBody)

			short.assertInvariants(t, "short stream")
			long.assertInvariants(t, "long stream")

			if long.totalInput() <= short.totalInput() {
				t.Errorf("expected long prompt streaming total input (%d) > short prompt streaming total input (%d)\nshort=%+v long=%+v",
					long.totalInput(), short.totalInput(), short, long)
			}

			refShort, refLong := reference(t)

			if (long.totalInput() > short.totalInput()) != (refLong.totalInput() > refShort.totalInput()) {
				t.Errorf("streaming total-input tendency disagrees with reference: "+
					"wingman short/long=%d/%d, reference short/long=%d/%d",
					short.totalInput(), long.totalInput(), refShort.totalInput(), refLong.totalInput())
			}
		})
	}
}

// TestUsageTokensThinking verifies the thinking-token split end-to-end: with
// extended thinking enabled, usage.output_tokens_details.thinking_tokens is
// reported as a subset of output_tokens, matching the reference account's
// behavior. Bedrock models are exempt from the thinking > 0 requirement — the
// AWS Converse API reports no reasoning bucket in its usage, so the spend
// stays folded into output_tokens there.
func TestUsageTokensThinking(t *testing.T) {
	h := anthropic.New(t)

	body := map[string]any{
		"max_tokens": 16000,
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 5000,
		},
		"messages": []map[string]any{
			{"role": "user", "content": "How many r's are in strawberry?"},
		},
	}

	var ref *messagesUsageResult
	reference := func(t *testing.T) messagesUsageResult {
		t.Helper()
		if ref == nil {
			u := messagesUsage(t, h, h.Anthropic, h.ReferenceModel, body)
			u.assertInvariants(t, "reference thinking")
			if u.thinking <= 0 {
				t.Fatalf("reference did not report thinking_tokens > 0 with thinking enabled: %+v", u)
			}
			ref = &u
		}
		return *ref
	}

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			u := messagesUsage(t, h, h.Wingman, model.Name, body)
			u.assertInvariants(t, "thinking")

			if strings.Contains(strings.ToLower(model.Name), "bedrock") {
				return
			}

			refUsage := reference(t)

			if u.thinking <= 0 {
				t.Errorf("expected thinking_tokens > 0 with thinking enabled (reference reports %d), got %+v",
					refUsage.thinking, u)
			}

			// The visible answer costs tokens on top of the thinking spend.
			if u.thinking > 0 && u.output <= u.thinking {
				t.Errorf("expected output_tokens (%d) > thinking_tokens (%d)", u.output, u.thinking)
			}
		})
	}
}

// referenceUsage returns a lazy, memoized fetch of the reference account's
// short/long usage. The reference does not depend on the model under test, so
// it is requested at most once per test (subtests run sequentially) and only
// when at least one model is actually configured. The reference's own
// invariants and tendency are asserted on first fetch.
func referenceUsage(shortBody, longBody map[string]any, fetch func(t *testing.T, body map[string]any) messagesUsageResult) func(t *testing.T) (messagesUsageResult, messagesUsageResult) {
	var cached *[2]messagesUsageResult

	return func(t *testing.T) (messagesUsageResult, messagesUsageResult) {
		t.Helper()

		if cached == nil {
			short := fetch(t, shortBody)
			long := fetch(t, longBody)

			short.assertInvariants(t, "reference short")
			long.assertInvariants(t, "reference long")

			if long.totalInput() <= short.totalInput() {
				t.Fatalf("reference did not show expected tendency: long total %d <= short total %d",
					long.totalInput(), short.totalInput())
			}

			cached = &[2]messagesUsageResult{short, long}
		}

		return cached[0], cached[1]
	}
}

type messagesUsageResult struct {
	input         int
	output        int
	cacheRead     int
	cacheCreation int
	thinking      int
}

// totalInput is the provider-neutral, cache-inclusive prompt cost: fresh input
// plus tokens served from or written to the cache.
func (u messagesUsageResult) totalInput() int {
	return u.input + u.cacheRead + u.cacheCreation
}

func (u messagesUsageResult) assertInvariants(t *testing.T, label string) {
	t.Helper()

	if u.input < 0 || u.cacheRead < 0 || u.cacheCreation < 0 {
		t.Errorf("[%s] negative token counts: input=%d read=%d creation=%d",
			label, u.input, u.cacheRead, u.cacheCreation)
	}
	if u.totalInput() <= 0 {
		t.Errorf("[%s] expected total input tokens > 0, got %d (%+v)", label, u.totalInput(), u)
	}
	if u.output <= 0 {
		t.Errorf("[%s] expected output_tokens > 0, got %d", label, u.output)
	}
	// thinking_tokens are a subset of output_tokens.
	if u.thinking < 0 || u.thinking > u.output {
		t.Errorf("[%s] thinking_tokens (%d) must be within [0, output_tokens=%d]", label, u.thinking, u.output)
	}
}

func messagesUsage(t *testing.T, h *anthropic.Harness, ep harness.Endpoint, model string, body map[string]any) messagesUsageResult {
	t.Helper()

	resp := anthropic.PostMessages(t, h, ep, anthropic.WithModel(body, model))
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", ep.Name, resp.StatusCode, string(resp.RawBody))
	}

	return messagesUsageFromMap(t, resp.Body["usage"])
}

func messagesUsageSSE(t *testing.T, h *anthropic.Harness, ep harness.Endpoint, model string, body map[string]any) messagesUsageResult {
	t.Helper()

	req := anthropic.WithModel(body, model)
	req["stream"] = true
	events := anthropic.PostMessagesSSE(t, h, ep, req)

	// Usage is spread across the stream: message_start carries the input-side
	// counts, the final message_delta the cumulative output count (and some
	// backends repeat the input fields there). Merge per field, later events win.
	merged := map[string]any{}
	deltaUsage := false

	for _, event := range events {
		var usage map[string]any

		switch event.Event {
		case "message_start":
			if msg, ok := event.Data["message"].(map[string]any); ok {
				usage, _ = msg["usage"].(map[string]any)
			}
		case "message_delta":
			usage, _ = event.Data["usage"].(map[string]any)
			deltaUsage = deltaUsage || usage != nil
		}

		for key, value := range usage {
			if value != nil {
				merged[key] = value
			}
		}
	}

	if !deltaUsage {
		t.Fatalf("[%s] no message_delta usage event found in %d events", ep.Name, len(events))
	}

	return messagesUsageFromMap(t, merged)
}

func messagesUsageFromMap(t *testing.T, usage any) messagesUsageResult {
	t.Helper()

	obj, ok := usage.(map[string]any)
	if !ok {
		t.Fatalf("usage is %T, want object", usage)
	}

	return messagesUsageResult{
		input:         getInt(t, obj, "input_tokens"),
		output:        getInt(t, obj, "output_tokens"),
		cacheRead:     getInt(t, obj, "cache_read_input_tokens"),
		cacheCreation: getInt(t, obj, "cache_creation_input_tokens"),
		thinking:      getInt(t, obj, "output_tokens_details", "thinking_tokens"),
	}
}

func buildLongUserPrompt(seed string) string {
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
