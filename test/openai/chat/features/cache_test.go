package features_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/chat"
)

// TestPromptCacheRead verifies that wingman propagates
// usage.prompt_tokens_details.cached_tokens on the second of two identical
// /v1/chat/completions calls.
//
// Caching is wingman-only: we don't compare against the reference OpenAI
// account because its independent cache state would diverge.
func TestPromptCacheRead(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Cache {
			continue
		}
		t.Run(model.Name, func(t *testing.T) {
			h.RequireConfigured(t, model.Name)
			systemPrompt := buildLongSystemPrompt(t.Name())

			body := map[string]any{
				"messages": []map[string]any{
					{"role": "system", "content": systemPrompt},
					{"role": "user", "content": "Reply with the single word: OK."},
				},
			}

			first, err := h.Client.Post(ctx, h.Wingman, "/chat/completions", chat.WithModel(body, model.Name))
			if err != nil {
				t.Fatalf("first call: %v", err)
			}
			if first.StatusCode != 200 {
				t.Fatalf("first call: status %d: %s", first.StatusCode, string(first.RawBody))
			}

			// Cache write propagation can take a moment; retry briefly.
			var lastUsage any
			for _, delay := range []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 3 * time.Second} {
				time.Sleep(delay)
				resp, err := h.Client.Post(ctx, h.Wingman, "/chat/completions", chat.WithModel(body, model.Name))
				if err != nil {
					t.Fatalf("retry call: %v", err)
				}
				if resp.StatusCode != 200 {
					t.Fatalf("retry call: status %d: %s", resp.StatusCode, string(resp.RawBody))
				}
				lastUsage = resp.Body["usage"]
				if cached := getInt(t, resp.Body, "usage", "prompt_tokens_details", "cached_tokens"); cached > 0 {
					return
				}
			}
			t.Fatalf("expected usage.prompt_tokens_details.cached_tokens > 0 on a follow-up call, never observed\nfirst usage: %v\nlast usage: %v",
				first.Body["usage"], lastUsage)
		})
	}
}

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
