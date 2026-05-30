package messages_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestWebSearchCompareHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What is the latest stable Go release? Cite an official source."},
				},
				"tools": []any{
					map[string]any{
						"type": "web_search_20250305",
						"name": "web_search",
					},
				},
			}

			anthropicResp := postAnthropic(t, h, h.Anthropic, withModel(body, h.ReferenceModel))
			if anthropicResp.StatusCode != 200 {
				t.Fatalf("anthropic status %d: %s", anthropicResp.StatusCode, string(anthropicResp.RawBody))
			}

			wingmanResp := postAnthropic(t, h, h.Wingman, withModel(body, model.Name))
			if wingmanResp.StatusCode != 200 {
				t.Fatalf("wingman status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
			}

			anthropicText := extractTextBlock(anthropicResp)
			wingmanText := extractTextBlock(wingmanResp)

			if anthropicText == "" {
				t.Error("anthropic response missing text content")
			}
			if wingmanText == "" {
				t.Error("wingman response missing text content")
			}

			anthropicServerToolUse := hasContentBlockType(anthropicResp, "server_tool_use")
			wingmanServerToolUse := hasContentBlockType(wingmanResp, "server_tool_use")

			t.Logf("anthropic server_tool_use present: %v (v1 wingman intentionally does not emit this)", anthropicServerToolUse)
			t.Logf("wingman server_tool_use present: %v", wingmanServerToolUse)
		})
	}
}

func extractTextBlock(resp *harness.RawResponse) string {
	content, _ := resp.Body["content"].([]any)
	for _, block := range content {
		obj, _ := block.(map[string]any)
		if obj["type"] != "text" {
			continue
		}
		if text, _ := obj["text"].(string); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func hasContentBlockType(resp *harness.RawResponse, blockType string) bool {
	content, _ := resp.Body["content"].([]any)
	for _, block := range content {
		obj, _ := block.(map[string]any)
		if obj["type"] == blockType {
			return true
		}
	}
	return false
}
