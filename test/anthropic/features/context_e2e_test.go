package features_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	claude "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	server "github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/go-chi/chi/v5"
)

// Runs the real provider behind local Wingman handlers, including signature
// scoping, without depending on a separately running or stale Wingman process.
// CLAUDE_CONTEXT_LIVE=1 go test ./test/anthropic/features -run TestClaudeContextE2E -v
func TestClaudeContextE2E(t *testing.T) {
	if os.Getenv("CLAUDE_CONTEXT_LIVE") != "1" {
		t.Skip("set CLAUDE_CONTEXT_LIVE=1 for paid Claude context tests")
	}
	h := anthropic.New(t)
	h.Client.Timeout = 180 * time.Second
	model := "claude-sonnet-4-6"
	systemModel := "claude-opus-5"
	cfg := &config.Config{Policy: noop.New()}
	for _, name := range []string{model, systemModel} {
		p, err := claude.NewCompleter(strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), name, claude.WithToken(h.Anthropic.APIKey), claude.WithMaxRetries(0))
		if err != nil {
			t.Fatal(err)
		}
		cfg.RegisterCompleter(name, signatures.ScopedTo(name, p))
	}
	router := chi.NewRouter()
	server.New(cfg).Attach(router)
	responses.New(cfg).Attach(router)
	local := httptest.NewServer(router)
	defer local.Close()
	h.Wingman = harness.Endpoint{Name: "wingman", BaseURL: local.URL}
	post := func(t *testing.T, ep harness.Endpoint, path string, body map[string]any) map[string]any {
		t.Helper()
		var result *harness.RawResponse
		if path == "/messages" {
			result = anthropic.PostMessages(t, h, ep, body)
		} else {
			var err error
			result, err = h.Client.Post(context.Background(), ep, path, body)
			if err != nil {
				t.Fatal(err)
			}
		}
		if result.StatusCode != 200 {
			t.Fatalf("%s %s returned %d: %s", ep.Name, path, result.StatusCode, result.RawBody)
		}
		return result.Body
	}
	for _, tc := range []struct {
		name, path string
		ep         harness.Endpoint
		stream     bool
	}{
		{"reference", "/messages", h.Anthropic, false},
		{"messages_http", "/messages", h.Wingman, false},
		{"messages_sse", "/messages", h.Wingman, true},
		{"responses_http", "/responses", h.Wingman, false},
		{"responses_sse", "/responses", h.Wingman, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": model, "stream": tc.stream}
			if tc.path == "/messages" {
				body["max_tokens"] = 4096
				body["messages"] = []any{map[string]any{"role": "user", "content": "Remember this project code for later: ALPHA-7. We are building a recipe app."}, map[string]any{"role": "assistant", "content": "The recipe app's project code is ALPHA-7."}}
				body["compaction"] = map[string]any{"type": "summarize"}
			} else {
				body["max_output_tokens"] = 4096
				body["input"] = []any{map[string]any{"role": "user", "content": "Remember this project code for later: ALPHA-7. We are building a recipe app."}, map[string]any{"role": "assistant", "content": "The recipe app's project code is ALPHA-7."}, map[string]any{"type": "compaction_trigger"}}
			}
			var block map[string]any
			if !tc.stream {
				result := post(t, tc.ep, tc.path, body)
				field := "content"
				if tc.path == "/responses" {
					field = "output"
				} else if result["stop_reason"] != "compaction" {
					t.Fatalf("expected compaction stop: %v", result["stop_reason"])
				}
				items, _ := result[field].([]any)
				if len(items) != 1 {
					t.Fatalf("expected summary only, got %v", items)
				}
				block = items[0].(map[string]any)
				if tc.ep.Name == "wingman" {
					usage := result["usage"].(map[string]any)
					if usage["output_tokens"].(float64) <= 0 {
						t.Fatal("compaction usage was omitted")
					}
				}
			} else {
				var events []*harness.SSEEvent
				if tc.path == "/messages" {
					events = anthropic.PostMessagesSSE(t, h, tc.ep, body)
				} else {
					var err error
					events, err = h.Client.PostSSE(context.Background(), tc.ep, tc.path, body)
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, event := range events {
					if event.Event == "content_block_start" {
						block, _ = event.Data["content_block"].(map[string]any)
					}
					if event.Event == "content_block_delta" {
						t.Fatal("signed summary unexpectedly arrived in deltas")
					}
					if event.Event == "response.output_item.done" {
						block, _ = event.Data["item"].(map[string]any)
					}
				}
			}
			if block == nil || block["type"] != "compaction" {
				t.Fatalf("no compaction block: %v", block)
			}
			if block["signature"] == nil && block["encrypted_content"] == nil {
				t.Fatal("missing continuation signature")
			}
			body["stream"] = false
			if tc.path == "/messages" {
				delete(body, "compaction")
				body["messages"] = []any{map[string]any{"role": "assistant", "content": []any{block}}, map[string]any{"role": "user", "content": "What is our project code? Reply with only the code."}}
			} else {
				body["input"] = []any{block, map[string]any{"role": "user", "content": "What is our project code? Reply with only the code."}}
			}
			result := post(t, tc.ep, tc.path, body)
			data, _ := json.Marshal(result)
			if !strings.Contains(string(data), "ALPHA-7") {
				t.Fatalf("compaction lost context: %s", data)
			}
		})
	}
	t.Run("threshold", func(t *testing.T) {
		body := map[string]any{"model": model, "max_tokens": 2048, "messages": buildAnthropicCompactionInput(), "context_management": map[string]any{"edits": []any{map[string]any{"type": "compact_20260112", "trigger": map[string]any{"type": "input_tokens", "value": 50000}}}}}
		result := post(t, h.Wingman, "/messages", body)
		requireCompactionBlock(t, "wingman", result)
		delete(body, "context_management")
		body["messages"] = []any{map[string]any{"role": "assistant", "content": result["content"]}, map[string]any{"role": "user", "content": "What was the secret code? Reply with only the code."}}
		result = post(t, h.Wingman, "/messages", body)
		data, _ := json.Marshal(result["content"])
		if !strings.Contains(string(data), "ALPHA-7") {
			t.Fatalf("threshold compaction lost context: %s", data)
		}
	})
	t.Run("mid_conversation_system", func(t *testing.T) {
		for _, ep := range []harness.Endpoint{h.Anthropic, h.Wingman} {
			t.Run(ep.Name, func(t *testing.T) {
				body := map[string]any{"model": systemModel, "max_tokens": 1024, "thinking": map[string]any{"type": "disabled"}, "system": "You are a helpful software project assistant. Be concise.", "messages": []any{
					map[string]any{"role": "user", "content": "We are building a recipe app."},
					map[string]any{"role": "assistant", "content": "I can help with the recipe app."},
					map[string]any{"role": "user", "content": "What is the current project code?"},
					map[string]any{"role": "system", "content": "The project code has just been assigned: ALPHA-7. Include it in the next status reply."},
				}}
				result := post(t, ep, "/messages", body)
				if result["stop_reason"] != "end_turn" {
					t.Fatalf("system-message probe did not complete: stop_reason=%v", result["stop_reason"])
				}
				if !strings.Contains(fmt.Sprint(result["content"]), "ALPHA-7") {
					t.Fatalf("mid-conversation system instruction ignored: %v", result["content"])
				}
			})
		}
	})
}
