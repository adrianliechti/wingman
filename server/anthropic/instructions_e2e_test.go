package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestInstructionLifetimeAcrossProvidersE2E(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	for _, backend := range []string{"claude", "claude-fallback", "responses", "chat", "gemini", "xai", "bedrock"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", backend, stream), func(t *testing.T) {
				var sent map[string]any
				var headers http.Header
				p := featureBackend(t, backend, func(r *http.Request, body map[string]any) { sent, headers = body, r.Header })
				router := featureRouter(p)
				history := []any{
					map[string]any{"role": "user", "content": "Start"},
					map[string]any{"role": "system", "content": "EXPIRED-INSTRUCTION", "clear_at": "next_user_message"},
					map[string]any{"role": "assistant", "content": "First answer"},
					map[string]any{"role": "user", "content": "Continue"},
					map[string]any{"role": "system", "content": "PERSISTENT-INSTRUCTION"},
					map[string]any{"role": "system", "content": "ACTIVE-INSTRUCTION", "clear_at": "next_user_message"},
				}
				body := map[string]any{"model": "target", "max_tokens": 1024, "stream": stream, "messages": history}
				for _, nextTurn := range []bool{false, true} {
					if nextTurn {
						body["messages"] = append(history, map[string]any{"role": "assistant", "content": "Second answer"}, map[string]any{"role": "user", "content": "Again"})
					}
					rec := featurePost(t, router, "/messages", body)
					if rec.Code != 200 || !strings.Contains(rec.Body.String(), "done") {
						t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
					}
					data, _ := json.Marshal(sent)
					wire := string(data)
					if !strings.Contains(wire, "PERSISTENT-INSTRUCTION") {
						t.Fatalf("lost standing instructions: %s", wire)
					}
					if backend == "claude" {
						if !strings.Contains(headers.Get("anthropic-beta"), "mid-conversation-system-clear-at-2026-08-21") {
							t.Fatal("missing native lifetime beta")
						}
						messages := sent["messages"].([]any)
						for _, index := range []int{1, 5} {
							if messages[index].(map[string]any)["clear_at"] != "next_user_message" {
								t.Fatal("lost native expiry or changed signed history")
							}
						}
					} else if strings.Contains(wire, "EXPIRED-INSTRUCTION") || strings.Contains(wire, "ACTIVE-INSTRUCTION") == nextTurn || strings.Contains(wire, "clear_at") {
						t.Fatalf("incorrect fallback lifetime: %s", wire)
					}
				}
			})
		}
	}
}

func TestOptionalProviderHintsRemainBestEffortE2E(t *testing.T) {
	p := featureBackend(t, "responses", func(*http.Request, map[string]any) {})
	request := `{"model":"target","max_tokens":1024,"top_p":0.9,"top_k":20,"metadata":{"user_id":"example"},"cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"user","content":[{"type":"text","text":"Hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`
	var body map[string]any
	json.Unmarshal([]byte(request), &body)
	rec := featurePost(t, featureRouter(p), "/messages", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "done") {
		t.Fatalf("optional hints broke a portable request: %d %s", rec.Code, rec.Body)
	}
}
