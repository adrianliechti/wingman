package compatibility_test

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestPortableCacheRequests(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "gpt-5.5", "claude-sonnet-4-6"} {
		t.Run(model, func(t *testing.T) {
			var sent map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.UnmarshalRead(r.Body, &sent); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if model == "claude-sonnet-4-6" {
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"claude-sonnet-4-6\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				} else {
					fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
				}
			}))
			t.Cleanup(upstream.Close)
			var backend provider.Completer
			var err error
			if model == "claude-sonnet-4-6" {
				backend, err = anthropic.NewCompleter(upstream.URL, model, anthropic.WithToken("test"), anthropic.WithMaxRetries(0))
			} else {
				backend, err = openai.NewResponder(upstream.URL, model, openai.WithToken("test"), openai.WithMaxRetries(0))
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter(model, backend)
			ep, client := localServer(t, cfg), harness.NewClient()
			for _, path := range []string{"/responses", "/chat/completions"} {
				for _, ttl := range []string{"7d", "12h"} {
					body := map[string]any{"model": model, "input": "hello", "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "prompt_cache_options": map[string]any{"ttl": ttl}}
					sent = nil
					result := post(t, client, ep, path, body, nil)
					if ttl == "7d" {
						requireStatus(t, path, result, 400)
						if sent != nil {
							t.Fatal("invalid TTL reached upstream")
						}
						continue
					}
					requireStatus(t, path, result, 200)
					var got any
					want := "12h"
					switch model {
					case "gpt-5.5":
						got, want = sent["prompt_cache_retention"], "24h"
					case "claude-sonnet-4-6":
						got, want = field(sent, "cache_control", "ttl"), "1h"
					default:
						got = field(sent, "prompt_cache_options", "ttl")
					}
					if got != want {
						t.Fatalf("%s extended cache = %v, want %s: %v", path, got, want, sent)
					}
				}
			}
		})
	}
}
