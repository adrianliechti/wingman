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

	reference := referenceUsage(shortBody, longBody, func(t *testing.T, body map[string]any) chatUsageResult {
		return chatUsage(t, ctx, h, h.OpenAI, h.ReferenceModel, body)
	})

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

			refShort, refLong := reference(t)

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

	reference := referenceUsage(shortBody, longBody, func(t *testing.T, body map[string]any) chatUsageResult {
		return chatUsageSSE(t, ctx, h, h.OpenAI, h.ReferenceModel, body)
	})

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := chatUsageSSE(t, ctx, h, h.Wingman, model.Name, shortBody)
			long := chatUsageSSE(t, ctx, h, h.Wingman, model.Name, longBody)

			short.assertInvariants(t, "short stream")
			long.assertInvariants(t, "long stream")

			if long.input <= short.input {
				t.Errorf("expected long prompt streaming prompt_tokens (%d) > short prompt streaming prompt_tokens (%d)",
					long.input, short.input)
			}

			refShort, refLong := reference(t)

			if (long.input > short.input) != (refLong.input > refShort.input) {
				t.Errorf("streaming prompt-token tendency disagrees with reference: "+
					"wingman short/long=%d/%d, reference short/long=%d/%d",
					short.input, long.input, refShort.input, refLong.input)
			}
		})
	}
}

// TestUsageTokensReasoning verifies the reasoning-token split end-to-end: with
// reasoning_effort set, completion_tokens_details.reasoning_tokens is reported
// as a subset of completion_tokens, matching the reference account's behavior.
// This holds cross-provider (claude reports thinking_tokens, gemini reports
// thoughtsTokenCount; both surface here as reasoning_tokens). Bedrock models
// are exempt from the reasoning > 0 requirement — the AWS Converse API reports
// no reasoning bucket in its usage, so the spend stays folded into
// completion_tokens there.
func TestUsageTokensReasoning(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	// The puzzle must be hard enough to reliably trigger thinking: claude maps
	// reasoning_effort to *adaptive* thinking, where the model itself decides
	// whether to think and skips it for trivial questions.
	body := map[string]any{
		"reasoning_effort": "high",
		"messages": []map[string]any{
			{"role": "user", "content": reasoningPuzzle},
		},
	}

	// A model on adaptive thinking may still occasionally answer without
	// thinking; retry a few times before judging. A systematic accounting
	// regression fails every attempt.
	usage := func(t *testing.T, ep harness.Endpoint, model string) chatUsageResult {
		t.Helper()
		var u chatUsageResult
		for range 3 {
			u = chatUsage(t, ctx, h, ep, model, body)
			if u.reasoning > 0 {
				break
			}
		}
		return u
	}

	var ref *chatUsageResult
	reference := func(t *testing.T) chatUsageResult {
		t.Helper()
		if ref == nil {
			u := usage(t, h.OpenAI, h.ReferenceModel)
			u.assertInvariants(t, "reference reasoning")
			if u.reasoning <= 0 {
				t.Fatalf("reference did not report reasoning_tokens > 0 with reasoning_effort set: %+v", u)
			}
			ref = &u
		}
		return *ref
	}

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			u := usage(t, h.Wingman, model.Name)
			u.assertInvariants(t, "reasoning")

			if strings.Contains(strings.ToLower(model.Name), "bedrock") {
				return
			}

			refUsage := reference(t)

			if u.reasoning <= 0 {
				t.Errorf("expected reasoning_tokens > 0 with reasoning_effort set (reference reports %d), got %+v",
					refUsage.reasoning, u)
			}

			// The visible answer costs tokens on top of the reasoning spend.
			if u.reasoning > 0 && u.output <= u.reasoning {
				t.Errorf("expected completion_tokens (%d) > reasoning_tokens (%d)", u.output, u.reasoning)
			}
		})
	}
}

// referenceUsage returns a lazy, memoized fetch of the reference account's
// short/long usage. The reference does not depend on the model under test, so
// it is requested at most once per test (subtests run sequentially) and only
// when at least one model is actually configured. The reference's own
// invariants and tendency are asserted on first fetch.
func referenceUsage(shortBody, longBody map[string]any, fetch func(t *testing.T, body map[string]any) chatUsageResult) func(t *testing.T) (chatUsageResult, chatUsageResult) {
	var cached *[2]chatUsageResult

	return func(t *testing.T) (chatUsageResult, chatUsageResult) {
		t.Helper()

		if cached == nil {
			short := fetch(t, shortBody)
			long := fetch(t, longBody)

			short.assertInvariants(t, "reference short")
			long.assertInvariants(t, "reference long")

			if long.input <= short.input {
				t.Fatalf("reference did not show expected tendency: long prompt %d <= short prompt %d",
					long.input, short.input)
			}

			cached = &[2]chatUsageResult{short, long}
		}

		return cached[0], cached[1]
	}
}

type chatUsageResult struct {
	input     int
	output    int
	total     int
	cached    int
	reasoning int
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
	// reasoning_tokens are a subset of completion_tokens.
	if u.reasoning < 0 || u.reasoning > u.output {
		t.Errorf("[%s] reasoning_tokens (%d) must be within [0, completion_tokens=%d]", label, u.reasoning, u.output)
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

func chatUsageSSE(t *testing.T, ctx context.Context, h *openai.Harness, ep harness.Endpoint, model string, body map[string]any) chatUsageResult {
	t.Helper()

	req := chat.WithModel(body, model)
	req["stream"] = true
	req["stream_options"] = map[string]any{"include_usage": true}
	events, err := h.Client.PostSSE(ctx, ep, "/chat/completions", req)
	if err != nil {
		t.Fatalf("[%s] SSE request: %v", ep.Name, err)
	}

	for _, event := range events {
		if usage, ok := event.Data["usage"]; ok && usage != nil {
			return chatUsageFromMap(t, usage)
		}
	}

	t.Fatalf("[%s] no streaming usage chunk found in %d events", ep.Name, len(events))
	return chatUsageResult{}
}

func chatUsageFromMap(t *testing.T, usage any) chatUsageResult {
	t.Helper()

	obj, ok := usage.(map[string]any)
	if !ok {
		t.Fatalf("usage is %T, want object", usage)
	}

	return chatUsageResult{
		input:     getInt(t, obj, "prompt_tokens"),
		output:    getInt(t, obj, "completion_tokens"),
		total:     getInt(t, obj, "total_tokens"),
		cached:    getInt(t, obj, "prompt_tokens_details", "cached_tokens"),
		reasoning: getInt(t, obj, "completion_tokens_details", "reasoning_tokens"),
	}
}

// reasoningPuzzle needs a couple of solving steps so adaptive-thinking models
// reliably engage thinking, while the visible answer stays short.
const reasoningPuzzle = "Alice, Bob and Carol have 17 coins between them. Alice has twice as many as Bob, and Carol has 3 fewer than Alice. How many coins does each person have? Answer with just the three numbers."

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
