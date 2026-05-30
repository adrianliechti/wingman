package responses_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
)

// TestWebSearchAgentCompareHTTP exercises the wingman agent-chain path
// end-to-end and compares it to OpenAI's native server-side web_search.
//
// Required setup:
//   TEST_OPENAI_AGENT_MODEL=<id of a wingman `chains:` entry of type agent
//                            with search/scrape tools attached>
//
// Skipped when the env var is unset.
func TestWebSearchAgentCompareHTTP(t *testing.T) {
	agentModel := os.Getenv("TEST_OPENAI_AGENT_MODEL")
	if agentModel == "" {
		t.Skip("TEST_OPENAI_AGENT_MODEL not set — skipping agent compare")
	}

	h := openai.New(t)
	ctx := context.Background()

	question := "What is the latest stable Go release? Cite an official source URL."

	openaiBody := map[string]any{
		"model": h.ReferenceModel,
		"input": question,
		"tools": []any{map[string]any{"type": "web_search"}},
	}

	wingmanBody := map[string]any{
		"model": agentModel,
		"input": question,
	}

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
		t.Error("wingman agent response missing assistant text")
	}

	t.Logf("openai answered (%d chars), web_search_call present: %v", len(openaiText), hasOutputItemType(openaiResp, "web_search_call"))
	t.Logf("wingman agent answered (%d chars)", len(wingmanText))

	if !mentionsSource(wingmanText) {
		t.Errorf("wingman agent answer did not mention any URL — agent may not have invoked the tool")
	}
}

func mentionsSource(text string) bool {
	return strings.Contains(text, "http://") || strings.Contains(text, "https://")
}
