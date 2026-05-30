package messages_test

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

func TestWebSearchAnthropicReference(t *testing.T) {
	h := anthropic.New(t)

	body := map[string]any{
		"model":      h.ReferenceModel,
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": "What is the latest stable Go release? Cite an official source."},
		},
		"tools": []any{
			map[string]any{
				"type":     "web_search_20250305",
				"name":     "web_search",
				"max_uses": 3,
			},
		},
	}

	resp := postAnthropic(t, h, h.Anthropic, body)
	if resp.StatusCode != 200 {
		t.Fatalf("anthropic returned status %d: %s", resp.StatusCode, string(resp.RawBody))
	}

	pretty, _ := json.MarshalIndent(resp.Body, "", "  ")
	t.Logf("Anthropic web_search reference response:\n%s", pretty)

	content, ok := resp.Body["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content is not a non-empty array; got: %v", resp.Body["content"])
	}

	var sawServerToolUse, sawSearchResult, sawCitation bool
	for _, block := range content {
		obj, _ := block.(map[string]any)
		switch obj["type"] {
		case "server_tool_use":
			sawServerToolUse = true
			if obj["name"] != "web_search" {
				t.Errorf("server_tool_use name=%v, want web_search", obj["name"])
			}
		case "web_search_tool_result":
			sawSearchResult = true
		case "text":
			cits, _ := obj["citations"].([]any)
			for _, c := range cits {
				cm, _ := c.(map[string]any)
				if cm["type"] == "web_search_result_location" {
					sawCitation = true
				}
			}
		}
	}

	if !sawServerToolUse {
		t.Errorf("expected at least one server_tool_use block")
	}
	if !sawSearchResult {
		t.Errorf("expected at least one web_search_tool_result block")
	}
	if !sawCitation {
		t.Logf("note: no web_search_result_location citation (model may not have cited)")
	}
}
