package claudecode

import (
	"context"
	"iter"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	server "github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/go-chi/chi/v5"
)

type trailingSignatureCompleter struct{}

func (trailingSignatureCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for _, content := range []provider.Content{
			provider.TextContent("WINGMAN_"),
			provider.TextContent("E2E_OK"),
			provider.ReasoningContent(provider.Reasoning{ID: "gemsig_fixture", Signature: "opaque-state"}),
		} {
			if !yield(&provider.Completion{
				Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{content}},
				Usage:   &provider.Usage{InputTokens: 10, OutputTokens: 5},
			}, nil) {
				return
			}
		}
	}
}

// Reproduce Gemini's trailing signature through the real Wingman handler and
// installed Claude Code without a network call to a model provider.
func TestClaudeCodeOfflineTrailingSignature(t *testing.T) {
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude Code is not installed")
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("claude-sonnet-4-6", trailingSignatureCompleter{})
	router := chi.NewRouter()
	router.Route("/v1", server.New(cfg).Attach)
	upstream := httptest.NewServer(router)
	t.Cleanup(upstream.Close)
	answer, exchanges, _ := runClaude(t, binary, harness.Endpoint{Name: "offline", BaseURL: upstream.URL + "/v1", APIKey: "test"}, "claude-sonnet-4-6", "Reply with WINGMAN_E2E_OK.", "", "")
	if answer != "WINGMAN_E2E_OK" {
		t.Errorf("trailing signature displaced the answer: %q", answer)
	}
	checkExchanges(t, exchanges, "claude-sonnet-4-6")
}
