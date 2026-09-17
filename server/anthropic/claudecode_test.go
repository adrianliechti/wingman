package anthropic

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	upstream "github.com/adrianliechti/wingman/pkg/provider/anthropic"
)

// Exercise the envelope emitted by Claude Code, including advisory metadata,
// cache hints and the explicit thinking-retention policy on every request.
func TestClaudeCodeRequestE2E(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "http"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			client := featureClient(t, claudeFeatureStream, func(req *http.Request, body map[string]any) {
				calls++
				if body["metadata"] != nil {
					t.Error("client metadata should not be forwarded as provider identity")
				}
				if body["max_tokens"] != float64(2048) || body["output_config"].(map[string]any)["effort"] != "low" {
					t.Errorf("lost generation controls: %v", body)
				}
				edits := body["context_management"].(map[string]any)["edits"].([]any)
				want := []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}
				if !reflect.DeepEqual(edits, want) || !slices.Contains(req.Header.Values("Anthropic-Beta"), "context-management-2025-06-27") {
					t.Errorf("lost thinking retention or beta: %v, %v", edits, req.Header.Values("Anthropic-Beta"))
				}
			})
			p, err := upstream.NewCompleter("https://upstream.invalid", "claude-sonnet-4-6", upstream.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			router := featureRouter(p)
			var body map[string]any
			if err := json.Unmarshal([]byte(`{
				"model":"target", "max_tokens":2048,
				"metadata":{"user_id":"fixture-user"},
				"system":[{"type":"text","text":"Fixture instructions","cache_control":{"type":"ephemeral"}}],
				"messages":[{"role":"user","content":[{"type":"text","text":"Hello","cache_control":{"type":"ephemeral"}}]}],
				"tools":[{"name":"Read","input_schema":{"type":"object","properties":{}},"cache_control":{"type":"ephemeral"}}],
				"thinking":{"type":"adaptive"}, "output_config":{"effort":"low"},
				"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}
			}`), &body); err != nil {
				t.Fatal(err)
			}
			body["stream"] = stream
			rec := featurePost(t, router, "/messages", body)
			if rec.Code != http.StatusOK || calls != 1 {
				t.Fatalf("Claude Code request failed: %d %s (upstream calls: %d)", rec.Code, rec.Body, calls)
			}
			rec = featurePost(t, router, "/messages/count_tokens", body)
			var count CountTokensResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &count); err != nil || rec.Code != http.StatusOK || count.InputTokens <= 0 || calls != 1 {
				t.Fatalf("token count failed: %d %s (%v)", rec.Code, rec.Body, err)
			}
		})
	}
}

func TestThinkingRetentionOptions(t *testing.T) {
	for _, tc := range []struct {
		keep string
		want provider.ReasoningContext
	}{
		{"", provider.ReasoningContextAuto},
		{`"all"`, provider.ReasoningContextAllTurns},
		{`{"type":"all"}`, provider.ReasoningContextAllTurns},
		{`{"type":"thinking_turns","value":1}`, provider.ReasoningContextCurrentTurn},
		{`{"type":"thinking_turns","value":0}`, ""},
		{`{"type":"thinking_turns","value":2}`, ""},
		{`"invalid"`, ""},
	} {
		t.Run(tc.keep, func(t *testing.T) {
			options, err := toCompleteOptions(MessageRequest{ContextManagement: &ContextManagement{Edits: []ContextManagementEdit{{Type: "clear_thinking_20251015", Keep: json.RawMessage(tc.keep)}}}})
			if tc.want == "" {
				if err == nil {
					t.Fatal("accepted unsupported thinking retention")
				}
				return
			}
			if err != nil || options.ReasoningOptions.Context != tc.want || options.CompactionOptions != nil {
				t.Fatalf("incorrect retention options: %+v, %v", options, err)
			}
		})
	}
}
