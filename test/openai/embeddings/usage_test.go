package embeddings_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
)

// TestUsageTokensCorrectness exercises usage accounting on /v1/embeddings.
//
// Unlike the chat surfaces, embeddings are deterministic: wingman forwards the
// input unchanged to the same model the reference is queried with, so token
// counts must match the reference exactly — not just in tendency.
func TestUsageTokensCorrectness(t *testing.T) {
	h := openai.New(t)
	h.SkipUnlessConfigured(t, embeddingModel)

	single := embeddingsUsagePairFor(t, h, map[string]any{"input": "Hello world"})
	long := embeddingsUsagePairFor(t, h, map[string]any{"input": buildLongEmbeddingInput()})
	batch := embeddingsUsagePairFor(t, h, map[string]any{"input": []string{"Hello", "World", "Test"}})

	for _, tc := range []struct {
		label string
		pair  embeddingsUsagePair
	}{
		{"single", single},
		{"long", long},
		{"batch", batch},
	} {
		tc.pair.wingman.assertInvariants(t, tc.label+" wingman")
		tc.pair.reference.assertInvariants(t, tc.label+" reference")

		if tc.pair.wingman != tc.pair.reference {
			t.Errorf("[%s] usage differs from reference: wingman=%+v reference=%+v",
				tc.label, tc.pair.wingman, tc.pair.reference)
		}
	}

	if long.wingman.prompt <= single.wingman.prompt {
		t.Errorf("expected long input prompt_tokens (%d) > single input prompt_tokens (%d)",
			long.wingman.prompt, single.wingman.prompt)
	}

	// A batch bills the sum of its inputs, so three inputs cost at least as
	// much as the single "Hello world".
	if batch.wingman.prompt < single.wingman.prompt {
		t.Errorf("expected batch prompt_tokens (%d) >= single prompt_tokens (%d)",
			batch.wingman.prompt, single.wingman.prompt)
	}
}

type embeddingsUsageResult struct {
	prompt int
	total  int
}

type embeddingsUsagePair struct {
	wingman   embeddingsUsageResult
	reference embeddingsUsageResult
}

func (u embeddingsUsageResult) assertInvariants(t *testing.T, label string) {
	t.Helper()

	if u.prompt <= 0 {
		t.Errorf("[%s] expected prompt_tokens > 0, got %d", label, u.prompt)
	}
	// Embeddings produce no completion tokens: total_tokens == prompt_tokens.
	if u.total != u.prompt {
		t.Errorf("[%s] total_tokens (%d) != prompt_tokens (%d)", label, u.total, u.prompt)
	}
}

func embeddingsUsagePairFor(t *testing.T, h *openai.Harness, body map[string]any) embeddingsUsagePair {
	t.Helper()

	openaiResp, wingmanResp := compareHTTP(t, h, openai.Model{Name: embeddingModel}, body)

	return embeddingsUsagePair{
		wingman:   embeddingsUsageFromBody(t, "wingman", wingmanResp.Body),
		reference: embeddingsUsageFromBody(t, "openai", openaiResp.Body),
	}
}

func embeddingsUsageFromBody(t *testing.T, label string, body map[string]any) embeddingsUsageResult {
	t.Helper()

	usage, ok := body["usage"].(map[string]any)
	if !ok {
		t.Fatalf("[%s] usage is %T, want object", label, body["usage"])
	}

	return embeddingsUsageResult{
		prompt: usageInt(usage, "prompt_tokens"),
		total:  usageInt(usage, "total_tokens"),
	}
}

func usageInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func buildLongEmbeddingInput() string {
	var b strings.Builder
	for range 40 {
		b.WriteString("This sentence exists purely to add tokens to the embedding input so the prompt token count is meaningfully larger. ")
	}
	return b.String()
}
