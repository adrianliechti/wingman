package responses_test

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
)

func TestWebSearchWingmanHTTP(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"model": model.Name,
				"input": "What is the latest stable Go release?",
				"tools": []any{map[string]any{"type": "web_search"}},
			}

			resp, err := h.Client.Post(ctx, h.Wingman, "/responses", body)
			if err != nil {
				t.Fatalf("wingman request failed: %v", err)
			}

			if resp.StatusCode != 200 {
				t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			output, ok := resp.Body["output"].([]any)
			if !ok || len(output) == 0 {
				t.Fatalf("expected output array; got: %v", resp.Body["output"])
			}

			var sawText bool
			for _, item := range output {
				obj, _ := item.(map[string]any)
				if obj["type"] == "message" {
					content, _ := obj["content"].([]any)
					for _, c := range content {
						cm, _ := c.(map[string]any)
						if text, _ := cm["text"].(string); strings.TrimSpace(text) != "" {
							sawText = true
						}
					}
				}
			}
			if !sawText {
				t.Errorf("expected assistant text in wingman response")
			}
		})
	}
}
