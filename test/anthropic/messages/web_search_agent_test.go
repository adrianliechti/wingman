package messages_test

import (
	"os"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

// TestWebSearchAgentCompareHTTP exercises the wingman agent-chain path
// end-to-end and compares it to Anthropic's native server-side web_search.
//
// Required setup:
//   TEST_ANTHROPIC_AGENT_MODEL=<id of a wingman `chains:` entry of type agent
//                                with search/scrape tools attached>
//
// Skipped when the env var is unset.
func TestWebSearchAgentCompareHTTP(t *testing.T) {
	agentModel := os.Getenv("TEST_ANTHROPIC_AGENT_MODEL")
	if agentModel == "" {
		t.Skip("TEST_ANTHROPIC_AGENT_MODEL not set — skipping agent compare")
	}

	h := anthropic.New(t)

	question := "What is the latest stable Go release? Cite an official source URL."

	anthropicBody := map[string]any{
		"model":      h.ReferenceModel,
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": question},
		},
		"tools": []any{
			map[string]any{
				"type": "web_search_20250305",
				"name": "web_search",
			},
		},
	}

	wingmanBody := map[string]any{
		"model":      agentModel,
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": question},
		},
	}

	anthropicResp := postAnthropic(t, h, h.Anthropic, anthropicBody)
	if anthropicResp.StatusCode != 200 {
		t.Fatalf("anthropic status %d: %s", anthropicResp.StatusCode, string(anthropicResp.RawBody))
	}

	wingmanResp := postAnthropic(t, h, h.Wingman, wingmanBody)
	if wingmanResp.StatusCode != 200 {
		t.Fatalf("wingman status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	anthropicText := extractTextBlock(anthropicResp)
	wingmanText := extractTextBlock(wingmanResp)

	if anthropicText == "" {
		t.Error("anthropic response missing text content")
	}
	if wingmanText == "" {
		t.Error("wingman agent response missing text content")
	}

	t.Logf("anthropic answered (%d chars), server_tool_use present: %v", len(anthropicText), hasContentBlockType(anthropicResp, "server_tool_use"))
	t.Logf("wingman agent answered (%d chars)", len(wingmanText))

	if !strings.Contains(wingmanText, "http://") && !strings.Contains(wingmanText, "https://") {
		t.Errorf("wingman agent answer did not mention any URL — agent may not have invoked the tool")
	}
}
