package base_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/chat"
)

// TestLegacyMaxTokens verifies the deprecated max_tokens parameter is honored.
// Wingman-only: the reference API rejects max_tokens on reasoning models, so
// there is nothing to compare against.
func TestLegacyMaxTokens(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.RequireConfigured(t, model.Name)

			body := chat.WithModel(map[string]any{
				"max_tokens": 4096,
				"messages": []map[string]any{
					{"role": "user", "content": "Reply with the single word: pong"},
				},
			}, model.Name)

			resp, err := h.Client.Post(context.Background(), h.Wingman, "/chat/completions", body)
			if err != nil {
				t.Fatalf("wingman request failed: %v", err)
			}

			if resp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			choices, _ := resp.Body["choices"].([]any)
			if len(choices) == 0 {
				t.Fatal("expected choices")
			}

			message, _ := choices[0].(map[string]any)["message"].(map[string]any)
			if content, _ := message["content"].(string); content == "" {
				t.Fatalf("expected content, got %v", message)
			}
		})
	}
}
