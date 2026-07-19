package tokens

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// TestCalibration replays recorded ground truth (Anthropic count_tokens for
// Claude families, exact tiktoken counts for GPT families; captured
// 2026-07-19 by the tokencounter fitting harness) and asserts the estimator
// stays within its documented accuracy: median ≤ 12%, worst ≤ 35%.
func TestCalibration(t *testing.T) {
	data, err := os.ReadFile("testdata/calibration.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]map[string]struct {
		Text   string `json:"text"`
		Tokens int64  `json:"tokens"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}

	for family, samples := range fixtures {
		var errs []float64
		for name, s := range samples {
			est := textForFamily(Family(family), s.Text)
			pct := 100 * math.Abs(float64(est)-float64(s.Tokens)) / float64(s.Tokens)
			errs = append(errs, pct)
			if pct > 35 {
				t.Errorf("%s/%s: estimate %d vs actual %d (%.1f%% > 35%%)", family, name, est, s.Tokens, pct)
			}
			t.Logf("%-13s %-15s actual=%-5d est=%-5d err=%.1f%%", family, name, s.Tokens, est, pct)
		}
		sort.Float64s(errs)
		median := errs[len(errs)/2]
		t.Logf(">>> %s: median error %.1f%%, worst %.1f%%", family, median, errs[len(errs)-1])
		if median > 12 {
			t.Errorf("%s: median error %.1f%% > 12%%", family, median)
		}
	}
}

func TestFamilyFor(t *testing.T) {
	cases := map[string]Family{
		"claude-sonnet-5":           Claude2026,
		"claude-opus-4-8":           Claude2026,
		"claude-haiku-4-5-20251001": ClaudeLegacy,
		"claude-opus-4-6":           ClaudeLegacy,
		"gpt-5.6":                   GPTO200k,
		"gpt-4o-2024-08-06":         GPTO200k,
		"o3-mini":                   GPTO200k,
		"gpt-4-turbo":               GPTCl100k,
		"gpt-3.5-turbo":             GPTCl100k,
		"mistral-large":             GPTO200k, // unknown → modern-BPE fallback
	}
	for model, want := range cases {
		if got := FamilyFor(model); got != want {
			t.Errorf("FamilyFor(%q) = %q, want %q", model, got, want)
		}
	}
}

// TestClaudeImage asserts the 28px-patch formula against live count_tokens
// measurements of real images (2026-07-19), ±8%.
func TestClaudeImage(t *testing.T) {
	cases := []struct {
		model    string
		w, h     int
		measured int
	}{
		{"claude-haiku-4-5", 896, 1280, 1482},
		{"claude-haiku-4-5", 1680, 3720, 1466},
		{"claude-haiku-4-5", 2680, 1748, 1578},
		{"claude-sonnet-5", 896, 1280, 1480},
		{"claude-sonnet-5", 1680, 3720, 3872},
		{"claude-sonnet-5", 2680, 1748, 4768},
	}
	for _, c := range cases {
		got := ClaudeImage(c.model, c.w, c.h)
		pct := 100 * math.Abs(float64(got)-float64(c.measured)) / float64(c.measured)
		t.Logf("%s %dx%d: est=%d measured=%d (%.1f%%)", c.model, c.w, c.h, got, c.measured, pct)
		if pct > 8 {
			t.Errorf("%s %dx%d: estimate %d vs measured %d (%.1f%% > 8%%)", c.model, c.w, c.h, got, c.measured, pct)
		}
	}
}

// TestOpenAIImage asserts tile math against /responses/input_tokens
// measurements (content tokens = measured minus the ~8-token message
// framing), ±5%.
func TestOpenAIImage(t *testing.T) {
	cases := []struct {
		model    string
		w, h     int
		low      bool
		measured int
	}{
		{"gpt-5", 896, 1280, true, 69},
		{"gpt-5", 896, 1280, false, 909},
		{"gpt-5", 1680, 3720, false, 1189},
		{"gpt-5", 2680, 1748, false, 909},
		{"gpt-4o", 896, 1280, true, 85},
		{"gpt-4o", 896, 1280, false, 1105},
		{"gpt-4o", 1680, 3720, false, 1445},
		{"gpt-4o", 2680, 1748, false, 1105},
	}
	for _, c := range cases {
		got := OpenAIImage(c.model, c.w, c.h, c.low)
		pct := 100 * math.Abs(float64(got)-float64(c.measured)) / float64(c.measured)
		t.Logf("%s %dx%d low=%v: est=%d measured=%d (%.1f%%)", c.model, c.w, c.h, c.low, got, c.measured, pct)
		if pct > 5 {
			t.Errorf("%s %dx%d low=%v: estimate %d vs measured %d", c.model, c.w, c.h, c.low, got, c.measured)
		}
	}
}

func TestPDFPages(t *testing.T) {
	pdf := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Pages /Kids [2 0 R 3 0 R] >>\nendobj\n" +
		"2 0 obj\n<< /Type /Page >>\nendobj\n3 0 obj\n<< /Type /Page >>\nendobj\n")
	if got := PDFPages(pdf); got != 2 {
		t.Errorf("PDFPages = %d, want 2", got)
	}
	if got := PDFPages([]byte("not a pdf")); got != 1 {
		t.Errorf("PDFPages fallback = %d, want 1", got)
	}
}

// TestEstimateCrossModel exercises the provider-struct API: the same common
// input is counted with the serving model's tokenizer and framing regardless
// of which API surface received the request. Bounds come from upstream
// measurements of a single user message "x" (Anthropic count_tokens: 7-8;
// OpenAI /responses/input_tokens: 7-8).
func TestEstimateCrossModel(t *testing.T) {
	in := Input{
		Messages: []provider.Message{{
			Role:    provider.MessageRoleUser,
			Content: []provider.Content{provider.TextContent("x")},
		}},
	}

	for _, model := range []string{"claude-sonnet-5", "claude-haiku-4-5", "gpt-5.6", "gpt-4o"} {
		got := Estimate(model, in)
		t.Logf("%s: %d tokens (upstream measured 7-8)", model, got)
		if got < 5 || got > 11 {
			t.Errorf("%s: Estimate = %d, want ≈7-8", model, got)
		}
	}

	// An assistant turn is free framing-wise on Anthropic but not on OpenAI —
	// measured: adding assistant "x" costs 1 token (Anthropic) vs ~6 (OpenAI).
	withAssistant := Input{
		Messages: append(in.Messages, provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{provider.TextContent("x")},
		}),
	}
	claudeDelta := Estimate("claude-haiku-4-5", withAssistant) - Estimate("claude-haiku-4-5", in)
	gptDelta := Estimate("gpt-5", withAssistant) - Estimate("gpt-5", in)
	t.Logf("assistant-message delta: claude=%d gpt=%d", claudeDelta, gptDelta)
	if claudeDelta >= gptDelta {
		t.Errorf("assistant framing should be cheaper on Anthropic (claude Δ%d vs gpt Δ%d)", claudeDelta, gptDelta)
	}
}
