package compatibility_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	ab "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	ah "github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestAnthropicCitationVersions(t *testing.T) {
	live(t)
	h := ah.New(t)
	model := "claude-sonnet-5-5"
	p, err := ab.NewCompleter(strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), model, ab.WithToken(h.Anthropic.APIKey), ab.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, p)
	h.Wingman = localServer(t, cfg)
	for _, ep := range []harness.Endpoint{h.Anthropic, h.Wingman} {
		r := post(t, h.Client, ep, "/messages/count_tokens", map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "Hi."}}}, http.Header{"Anthropic-Version": {"2026-10-22"}})
		requireStatus(t, ep.Name+" invalid version", r, 400)
	}
	for _, nested := range []bool{false, true} {
		for _, version := range []string{"2023-06-01", "2026-10-21"} {
			for _, path := range []string{"/messages/count_tokens", "/messages"} {
				cited := map[string]any{"type": "text", "text": "red", "citations": []any{map[string]any{"type": "char_location", "cited_text": "red", "document_index": 0, "document_title": "Color", "start_char_index": 0, "end_char_index": 3}}}
				var content any = cited
				if nested {
					content = map[string]any{"type": "search_result", "source": "https://example.com/color", "title": "Color", "content": []any{cited}}
				}
				body := map[string]any{"model": model, "tools": []any{map[string]any{"name": "lookup", "description": "Look up a color", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}}}}, "messages": []any{
					map[string]any{"role": "user", "content": "Look up a color. Then reply only OK."},
					map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_review", "name": "lookup", "input": map[string]any{}}}},
					map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_review", "content": []any{content}}}},
				}}
				if path == "/messages" {
					body["max_tokens"] = 32
				}
				for _, ep := range []harness.Endpoint{h.Anthropic, h.Wingman} {
					r := post(t, h.Client, ep, path, body, http.Header{"Anthropic-Version": {version}})
					want := 400
					if nested && version == "2023-06-01" {
						want = 200
					}
					requireStatus(t, fmt.Sprintf("%s version=%s nested=%t path=%s", ep.Name, version, nested, path), r, want)
				}
			}
		}
	}
}
