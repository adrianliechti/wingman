package messages_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

func TestWebFetchWingmanHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "Summarize https://go.dev/doc/devel/release in one sentence."},
				},
				"tools": []any{
					map[string]any{
						"type": "web_fetch_20250910",
						"name": "web_fetch",
					},
				},
			}

			resp := postAnthropic(t, h, h.Wingman, withModel(body, model.Name))
			if resp.StatusCode != 200 {
				t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			content, ok := resp.Body["content"].([]any)
			if !ok || len(content) == 0 {
				t.Fatalf("expected content array; got: %v", resp.Body["content"])
			}

			var sawText bool
			for _, block := range content {
				obj, _ := block.(map[string]any)
				if obj["type"] == "text" {
					if text, _ := obj["text"].(string); strings.TrimSpace(text) != "" {
						sawText = true
					}
				}
			}
			if !sawText {
				t.Errorf("expected assistant text block in wingman response")
			}
		})
	}
}
