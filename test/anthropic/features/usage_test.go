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
			refShort := messagesUsage(t, h, h.Anthropic, h.ReferenceModel, shortBody)
			refLong := messagesUsage(t, h, h.Anthropic, h.ReferenceModel, longBody)

			refShort.assertInvariants(t, "reference short")
			refLong.assertInvariants(t, "reference long")

			if refLong.totalInput() <= refShort.totalInput() {
				t.Fatalf("reference did not show expected tendency: long total %d <= short total %d",
					refLong.totalInput(), refShort.totalInput())
			}

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

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := messagesUsageSSE(t, h, model.Name, shortBody)
			long := messagesUsageSSE(t, h, model.Name, longBody)

			short.assertInvariants(t, "short stream")
			long.assertInvariants(t, "long stream")

			if long.totalInput() <= short.totalInput() {
				t.Errorf("expected long prompt streaming total input (%d) > short prompt streaming total input (%d)\nshort=%+v long=%+v",
					long.totalInput(), short.totalInput(), short, long)
			}
		})
	}
}

type messagesUsageResult struct {
	input         int
	output        int
	cacheRead     int
	cacheCreation int
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
}

func messagesUsage(t *testing.T, h *anthropic.Harness, ep harness.Endpoint, model string, body map[string]any) messagesUsageResult {
	t.Helper()

	resp := anthropic.PostMessages(t, h, ep, anthropic.WithModel(body, model))
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", ep.Name, resp.StatusCode, string(resp.RawBody))
	}

	return messagesUsageFromMap(t, resp.Body["usage"])
}

func messagesUsageSSE(t *testing.T, h *anthropic.Harness, model string, body map[string]any) messagesUsageResult {
	t.Helper()

	req := anthropic.WithModel(body, model)
	req["stream"] = true
	events := anthropic.PostMessagesSSE(t, h, h.Wingman, req)

	for _, event := range events {
		if event.Event != "message_delta" {
			continue
		}
		if usage, ok := event.Data["usage"]; ok {
			return messagesUsageFromMap(t, usage)
		}
	}

	t.Fatalf("no message_delta usage event found in %d events", len(events))
	return messagesUsageResult{}
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
