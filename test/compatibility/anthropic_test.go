package compatibility_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	backend "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/test/anthropic"
)

func TestAnthropicAPIVersion(t *testing.T) {
	live(t)
	h := anthropic.New(t)
	const model = "claude-sonnet-5-5"
	p, err := backend.NewCompleter(strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), model, backend.WithToken(h.Anthropic.APIKey), backend.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, signatures.ScopedTo(model, p))
	h.Wingman = localServer(t, cfg)

	for _, version := range []string{"2023-06-01", "2026-10-21"} {
		t.Run(version, func(t *testing.T) {
			for _, parameter := range []string{"temperature", "top_p", "top_k"} {
				t.Run(parameter, func(t *testing.T) {
					body := map[string]any{"model": model, "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": "Reply only OK."}}, parameter: 1}
					headers := http.Header{"Anthropic-Version": {version}}
					reference := post(t, h.Client, h.Anthropic, "/messages", body, headers)
					want := 200
					// Sonnet 5.5 rejects top_p/top_k on both API versions.
					if version == "2026-10-21" || parameter != "temperature" {
						want = 400
					}
					requireStatus(t, "reference", reference, want)
					actual := post(t, h.Client, h.Wingman, "/messages", body, headers)
					requireStatus(t, "wingman (sampling ignored)", actual, 200)
				})
			}
		})
	}
	for _, path := range []string{"/messages", "/messages/count_tokens"} {
		t.Run(path, func(t *testing.T) {
			for _, kind := range []string{"assistant_document", "user_citations"} {
				t.Run(kind, func(t *testing.T) {
					messages := []any{map[string]any{"role": "user", "content": "Reply only OK."}}
					if kind == "assistant_document" {
						messages = append(messages, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "document", "source": map[string]any{"type": "text", "media_type": "text/plain", "data": "The color is red."}}}}, map[string]any{"role": "user", "content": "What color?"})
					} else {
						messages = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "red", "citations": []any{map[string]any{"type": "char_location", "cited_text": "red", "document_index": 0, "document_title": "Color", "start_char_index": 0, "end_char_index": 3}}}}}}
					}
					body := map[string]any{"model": model, "messages": messages}
					if path == "/messages" {
						body["max_tokens"] = 32
					}
					headers := http.Header{"Anthropic-Version": {"2026-10-21"}}
					reference := post(t, h.Client, h.Anthropic, path, body, headers)
					requireStatus(t, "reference", reference, 400)
					actual := post(t, h.Client, h.Wingman, path, body, headers)
					requireStatus(t, "wingman", actual, reference.StatusCode)
				})
			}
		})
	}
}
