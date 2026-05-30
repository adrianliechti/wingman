package messages_test

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

func TestWebFetchAnthropicReference(t *testing.T) {
	h := anthropic.New(t)

	body := map[string]any{
		"model":      h.ReferenceModel,
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": "Summarize https://go.dev/doc/devel/release in two sentences."},
		},
		"tools": []any{
			map[string]any{
				"type":     "web_fetch_20250910",
				"name":     "web_fetch",
				"max_uses": 1,
				"citations": map[string]any{
					"enabled": true,
				},
			},
		},
	}

	resp := postAnthropic(t, h, h.Anthropic, body)
	if resp.StatusCode != 200 {
		t.Fatalf("anthropic returned status %d: %s", resp.StatusCode, string(resp.RawBody))
	}

	pretty, _ := json.MarshalIndent(resp.Body, "", "  ")
	t.Logf("Anthropic web_fetch reference response:\n%s", pretty)

	content, ok := resp.Body["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content is not a non-empty array; got: %v", resp.Body["content"])
	}

	var sawServerToolUse, sawFetchResult, sawCharCitation bool
	for _, block := range content {
		obj, _ := block.(map[string]any)
		switch obj["type"] {
		case "server_tool_use":
			sawServerToolUse = true
			if obj["name"] != "web_fetch" {
				t.Errorf("server_tool_use name=%v, want web_fetch", obj["name"])
			}
		case "web_fetch_tool_result":
			sawFetchResult = true
		case "text":
			cits, _ := obj["citations"].([]any)
			for _, c := range cits {
				cm, _ := c.(map[string]any)
				if cm["type"] == "char_location" {
					sawCharCitation = true
				}
			}
		}
	}

	if !sawServerToolUse {
		t.Errorf("expected at least one server_tool_use block")
	}
	if !sawFetchResult {
		t.Errorf("expected at least one web_fetch_tool_result block")
	}
	if !sawCharCitation {
		t.Logf("note: no char_location citation (model may not have cited)")
	}
}
