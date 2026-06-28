package features_test

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// TestUsageTokensCorrectness exercises usage accounting on /v1/responses.
//
// Token counts are never compared for equality against the reference OpenAI
// account — tokenizers and framing differ per provider. We assert
// self-consistent invariants on each wingman response and verify the same
// *tendency* (longer prompt costs more) as the reference.
func TestUsageTokensCorrectness(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	shortBody := map[string]any{
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": []map[string]any{{"type": "input_text", "text": "Reply with the single word: OK."}},
			},
		},
	}
	longBody := map[string]any{
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": []map[string]any{{"type": "input_text", "text": buildLongUserPrompt("responses-usage") + "\n\nReply with: OK"}},
			},
		},
	}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := responsesUsage(t, ctx, h, h.Wingman, model.Name, shortBody)
			long := responsesUsage(t, ctx, h, h.Wingman, model.Name, longBody)

			short.assertInvariants(t, "short")
			long.assertInvariants(t, "long")

			if long.input <= short.input {
				t.Errorf("expected long prompt input_tokens (%d) > short prompt input_tokens (%d)",
					long.input, short.input)
			}

			refShort := responsesUsage(t, ctx, h, h.OpenAI, h.ReferenceModel, shortBody)
			refLong := responsesUsage(t, ctx, h, h.OpenAI, h.ReferenceModel, longBody)

			refShort.assertInvariants(t, "reference short")
			refLong.assertInvariants(t, "reference long")

			if refLong.input <= refShort.input {
				t.Fatalf("reference did not show expected tendency: long input %d <= short input %d",
					refLong.input, refShort.input)
			}

			if (long.input > short.input) != (refLong.input > refShort.input) {
				t.Errorf("input-token tendency disagrees with reference: "+
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
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": []map[string]any{{"type": "input_text", "text": "Reply with the single word: OK."}},
			},
		},
	}
	longBody := map[string]any{
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": []map[string]any{{"type": "input_text", "text": buildLongUserPrompt("responses-usage-sse") + "\n\nReply with: OK"}},
			},
		},
	}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			short := responsesUsageSSE(t, ctx, h, model.Name, shortBody)
			long := responsesUsageSSE(t, ctx, h, model.Name, longBody)

			short.assertInvariants(t, "short stream")
			long.assertInvariants(t, "long stream")

			if long.input <= short.input {
				t.Errorf("expected long prompt streaming input_tokens (%d) > short prompt streaming input_tokens (%d)",
					long.input, short.input)
			}
		})
	}
}

type responsesUsageResult struct {
	input     int
	output    int
	total     int
	cached    int
	reasoning int
}

func (u responsesUsageResult) assertInvariants(t *testing.T, label string) {
	t.Helper()

	if u.input <= 0 {
		t.Errorf("[%s] expected input_tokens > 0, got %d", label, u.input)
	}
	if u.output <= 0 {
		t.Errorf("[%s] expected output_tokens > 0, got %d", label, u.output)
	}
	// Responses wire convention: total_tokens == input_tokens + output_tokens.
	if u.total != u.input+u.output {
		t.Errorf("[%s] total_tokens (%d) != input_tokens (%d) + output_tokens (%d)",
			label, u.total, u.input, u.output)
	}
	// cached_tokens are a subset of input_tokens (cache-inclusive convention).
	if u.cached < 0 || u.cached > u.input {
		t.Errorf("[%s] cached_tokens (%d) must be within [0, input_tokens=%d]", label, u.cached, u.input)
	}
	// reasoning_tokens are a subset of output_tokens.
	if u.reasoning < 0 || u.reasoning > u.output {
		t.Errorf("[%s] reasoning_tokens (%d) must be within [0, output_tokens=%d]", label, u.reasoning, u.output)
	}
}

func responsesUsage(t *testing.T, ctx context.Context, h *openai.Harness, ep harness.Endpoint, model string, body map[string]any) responsesUsageResult {
	t.Helper()

	resp, err := h.Client.Post(ctx, ep, "/responses", responses.WithModel(body, model))
	if err != nil {
		t.Fatalf("[%s] request: %v", ep.Name, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", ep.Name, resp.StatusCode, string(resp.RawBody))
	}

	return responsesUsageFromMap(t, resp.Body["usage"])
}

func responsesUsageSSE(t *testing.T, ctx context.Context, h *openai.Harness, model string, body map[string]any) responsesUsageResult {
	t.Helper()

	req := responses.WithModel(body, model)
	req["stream"] = true
	events, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", req)
	if err != nil {
		t.Fatalf("[wingman] SSE request: %v", err)
	}

	for _, event := range events {
		if event.Event != "response.completed" {
			continue
		}
		response, ok := event.Data["response"].(map[string]any)
		if !ok {
			continue
		}
		if usage, ok := response["usage"]; ok {
			return responsesUsageFromMap(t, usage)
		}
	}

	t.Fatalf("no response.completed usage found in %d events", len(events))
	return responsesUsageResult{}
}

func responsesUsageFromMap(t *testing.T, usage any) responsesUsageResult {
	t.Helper()

	obj, ok := usage.(map[string]any)
	if !ok {
		t.Fatalf("usage is %T, want object", usage)
	}

	return responsesUsageResult{
		input:     getInt(t, obj, "input_tokens"),
		output:    getInt(t, obj, "output_tokens"),
		total:     getInt(t, obj, "total_tokens"),
		cached:    getInt(t, obj, "input_tokens_details", "cached_tokens"),
		reasoning: getInt(t, obj, "output_tokens_details", "reasoning_tokens"),
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
