package features_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/anthropic"
)

// TestPromptCacheRead verifies that wingman propagates cache_read_input_tokens
// on the second of two identical calls when cache_control is set.
//
// Caching is wingman-only: we don't compare against the reference Anthropic
// account because its independent cache state would diverge.
func TestPromptCacheRead(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Cache {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			systemPrompt := buildLongSystemPrompt(t.Name())

			body := map[string]any{
				"max_tokens": 32,
				"system": []map[string]any{
					{
						"type":          "text",
						"text":          systemPrompt,
						"cache_control": map[string]any{"type": "ephemeral"},
					},
				},
				"messages": []map[string]any{
					{"role": "user", "content": "Reply with the single word: OK."},
				},
			}

			// First call — primes the cache.
			first := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(body, model.Name))
			if first.StatusCode != 200 {
				t.Fatalf("first call: status %d: %s", first.StatusCode, string(first.RawBody))
			}

			// Cache write propagation can take a moment; retry briefly.
			var lastUsage any
			for _, delay := range []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 3 * time.Second} {
				time.Sleep(delay)
				resp := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(body, model.Name))
				if resp.StatusCode != 200 {
					t.Fatalf("retry call: status %d: %s", resp.StatusCode, string(resp.RawBody))
				}
				lastUsage = resp.Body["usage"]
				if cacheRead := getInt(t, resp.Body, "usage", "cache_read_input_tokens"); cacheRead > 0 {
					assertAnthropicCacheAccounting(t, resp.Body)
					return
				}
			}
			t.Fatalf("expected cache_read_input_tokens > 0 on a follow-up call, never observed\nfirst usage: %v\nlast usage: %v",
				first.Body["usage"], lastUsage)
		})
	}
}

// buildLongSystemPrompt produces a deterministic prompt long enough (>1024 tokens)
// to be eligible for Anthropic prompt caching. Including a stable per-test
// suffix isolates concurrent test runs from one another.
func buildLongSystemPrompt(seed string) string {
	var b strings.Builder
	b.WriteString("You are a careful assistant in test scenario ")
	b.WriteString(seed)
	b.WriteString(". Reference manual follows.\n\n")

	for i := range 200 {
		fmt.Fprintf(&b,
			"Section %d: This paragraph exists solely to inflate the token count "+
				"of the cached system prompt above the minimum threshold required "+
				"by the provider for prompt caching to take effect. The text is "+
				"intentionally repetitive and carries no operational meaning.\n",
			i)
	}
	return b.String()
}

// assertAnthropicCacheAccounting verifies the Anthropic-wire usage convention:
// input_tokens excludes both cache-read and cache-creation tokens (those are
// reported in their own fields). This is a regression guard for the Bedrock
// path, which used to fold cache-read tokens into input_tokens and double-count
// them. Counts are not compared against the reference (cache state diverges);
// only the wire convention is asserted.
func assertAnthropicCacheAccounting(t *testing.T, body map[string]any) {
	t.Helper()

	input := getInt(t, body, "usage", "input_tokens")
	output := getInt(t, body, "usage", "output_tokens")
	cacheRead := getInt(t, body, "usage", "cache_read_input_tokens")
	cacheCreation := getInt(t, body, "usage", "cache_creation_input_tokens")

	if output <= 0 {
		t.Errorf("expected output_tokens > 0, got %d (usage: %v)", output, body["usage"])
	}

	// With a fully-cached system prompt, the fresh input_tokens should be small
	// relative to the cached tokens. The bug folded cache_read into input_tokens,
	// which would make input_tokens >= cacheRead. Assert it stays well below.
	if cacheRead > 0 && input >= cacheRead {
		t.Errorf("input_tokens (%d) should exclude cache_read_input_tokens (%d); "+
			"looks like cached tokens were double-counted (usage: %v)",
			input, cacheRead, body["usage"])
	}

	if input < 0 || cacheRead < 0 || cacheCreation < 0 {
		t.Errorf("negative token counts in usage: %v", body["usage"])
	}
}

func getInt(t *testing.T, m map[string]any, path ...string) int {
	t.Helper()
	var cur any = m
	for _, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = obj[key]
	}
	switch v := cur.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case nil:
		return 0
	default:
		return 0
	}
}
