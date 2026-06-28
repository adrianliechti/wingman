package features_test

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/chat"
)

// TestUsageTokensCorrectness exercises usage accounting on /v1/chat/completions.
//
// Token counts are never compared for equality against the reference OpenAI
// account — tokenizers and framing differ per provider. We assert
// self-consistent invariants on each wingman response and verify the same
// *tendency* (longer prompt costs more) as the reference.
func TestUsageTokensCorrectness(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	shortBody := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "Reply with the single word: OK."},
		},
	}
	longBody := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": buildLongUserPrompt("chat-usage") + "\n\nReply with the single word: OK."},
		},
	}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := chatUsage(t, ctx, h, h.Wingman, model.Name, shortBody)
			long := chatUsage(t, ctx, h, h.Wingman, model.Name, longBody)

			short.assertInvariants(t, "short")
			long.assertInvariants(t, "long")

			if long.input <= short.input {
				t.Errorf("expected long prompt prompt_tokens (%d) > short prompt prompt_tokens (%d)",
					long.input, short.input)
			}

			refShort := chatUsage(t, ctx, h, h.OpenAI, h.ReferenceModel, shortBody)
			refLong := chatUsage(t, ctx, h, h.OpenAI, h.ReferenceModel, longBody)

			refShort.assertInvariants(t, "reference short")
			refLong.assertInvariants(t, "reference long")

			if refLong.input <= refShort.input {
				t.Fatalf("reference did not show expected tendency: long prompt %d <= short prompt %d",
					refLong.input, refShort.input)
			}

			if (long.input > short.input) != (refLong.input > refShort.input) {
				t.Errorf("prompt-token tendency disagrees with reference: "+
					"wingman short/long=%d/%d, reference short/long=%d/%d",
					short.input, long.input, refShort.input, refLong.input)
			}
		})
	}
}

func TestUsageTokensCorrectnessSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	shortBody := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "Reply with the single word: OK."},
		},
	}
	longBody := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": buildLongUserPrompt("chat-usage-sse") + "\n\nReply with the single word: OK."},
		},
	}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := chatUsageSSE(t, ctx, h, model.Name, shortBody)
			long := chatUsageSSE(t, ctx, h, model.Name, longBody)

			short.assertInvariants(t, "short stream")
			long.assertInvariants(t, "long stream")

			if long.input <= short.input {
				t.Errorf("expected long prompt streaming prompt_tokens (%d) > short prompt streaming prompt_tokens (%d)",
					long.input, short.input)
			}
		})
	}
}

type chatUsageResult struct {
	input  int
	output int
	total  int
	cached int
}

func (u chatUsageResult) assertInvariants(t *testing.T, label string) {
	t.Helper()

	if u.input <= 0 {
		t.Errorf("[%s] expected prompt_tokens > 0, got %d", label, u.input)
	}
	if u.output <= 0 {
		t.Errorf("[%s] expected completion_tokens > 0, got %d", label, u.output)
	}
	// OpenAI wire convention: total_tokens == prompt_tokens + completion_tokens.
	if u.total != u.input+u.output {
		t.Errorf("[%s] total_tokens (%d) != prompt_tokens (%d) + completion_tokens (%d)",
			label, u.total, u.input, u.output)
	}
	// cached_tokens are a subset of prompt_tokens (cache-inclusive convention).
	if u.cached < 0 || u.cached > u.input {
		t.Errorf("[%s] cached_tokens (%d) must be within [0, prompt_tokens=%d]", label, u.cached, u.input)
	}
}

func chatUsage(t *testing.T, ctx context.Context, h *openai.Harness, ep harness.Endpoint, model string, body map[string]any) chatUsageResult {
	t.Helper()

	resp, err := h.Client.Post(ctx, ep, "/chat/completions", chat.WithModel(body, model))
	if err != nil {
		t.Fatalf("[%s] request: %v", ep.Name, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", ep.Name, resp.StatusCode, string(resp.RawBody))
	}

	return chatUsageFromMap(t, resp.Body["usage"])
}

func chatUsageSSE(t *testing.T, ctx context.Context, h *openai.Harness, model string, body map[string]any) chatUsageResult {
	t.Helper()

	req := chat.WithModel(body, model)
	req["stream"] = true
	req["stream_options"] = map[string]any{"include_usage": true}
	events, err := h.Client.PostSSE(ctx, h.Wingman, "/chat/completions", req)
	if err != nil {
		t.Fatalf("[wingman] SSE request: %v", err)
	}

	for _, event := range events {
		if usage, ok := event.Data["usage"]; ok && usage != nil {
			return chatUsageFromMap(t, usage)
		}
	}

	t.Fatalf("no streaming usage chunk found in %d events", len(events))
	return chatUsageResult{}
}

func chatUsageFromMap(t *testing.T, usage any) chatUsageResult {
	t.Helper()

	obj, ok := usage.(map[string]any)
	if !ok {
		t.Fatalf("usage is %T, want object", usage)
	}

	return chatUsageResult{
		input:  getInt(t, obj, "prompt_tokens"),
		output: getInt(t, obj, "completion_tokens"),
		total:  getInt(t, obj, "total_tokens"),
		cached: getInt(t, obj, "prompt_tokens_details", "cached_tokens"),
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
