package responses_test

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestWebSearchCompareHTTP(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "What is the latest stable Go release? Cite an official source.",
				"tools": []any{map[string]any{"type": "web_search"}},
			}

			openaiBody := withModel(body, h.ReferenceModel)
			wingmanBody := withModel(body, model.Name)

			openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/responses", openaiBody)
			if err != nil {
				t.Fatalf("openai request failed: %v", err)
			}
			if openaiResp.StatusCode != 200 {
				t.Fatalf("openai status %d: %s", openaiResp.StatusCode, string(openaiResp.RawBody))
			}

			wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/responses", wingmanBody)
			if err != nil {
				t.Fatalf("wingman request failed: %v", err)
			}
			if wingmanResp.StatusCode != 200 {
				t.Fatalf("wingman status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
			}

			openaiText := extractAssistantText(openaiResp)
			wingmanText := extractAssistantText(wingmanResp)

			if openaiText == "" {
				t.Error("openai response missing assistant text")
			}
			if wingmanText == "" {
				t.Error("wingman response missing assistant text")
			}

			openaiHasSearchCall := hasOutputItemType(openaiResp, "web_search_call")
			wingmanHasSearchCall := hasOutputItemType(wingmanResp, "web_search_call")

			t.Logf("openai web_search_call present: %v (v1 wingman intentionally does not emit this)", openaiHasSearchCall)
			t.Logf("wingman web_search_call present: %v", wingmanHasSearchCall)
		})
	}
}

func extractAssistantText(resp *harness.RawResponse) string {
	output, _ := resp.Body["output"].([]any)
	for _, item := range output {
		obj, _ := item.(map[string]any)
		if obj["type"] != "message" {
			continue
		}
		content, _ := obj["content"].([]any)
		for _, c := range content {
			cm, _ := c.(map[string]any)
			if text, _ := cm["text"].(string); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func hasOutputItemType(resp *harness.RawResponse, itemType string) bool {
	output, _ := resp.Body["output"].([]any)
	for _, item := range output {
		obj, _ := item.(map[string]any)
		if obj["type"] == itemType {
			return true
		}
	}
	return false
}
