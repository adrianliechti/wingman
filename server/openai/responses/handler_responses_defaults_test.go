package responses

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
)

// Defaults captured from gpt-5.4-mini on 2026-09-28. Generated content,
// usage, timestamps, provider-specific storage/billing, and removed sampling
// controls are excluded.
func TestResponseEnvelopeMatchesReference(t *testing.T) {
	data, err := os.ReadFile("testdata/reference_defaults.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference map[string]any
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"temperature", "top_p", "top_k"} {
		delete(reference, field)
	}
	// Effective billing metadata is tested separately; this fake backend
	// does not report a service tier.
	reference["service_tier"] = nil
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("test", reasoningStatusCompleter{
				content:          []provider.Content{provider.TextContent("Answer")},
				status:           provider.CompletionStatusCompleted,
				reasoningContext: provider.ReasoningContextCurrentTurn,
			})
			request, err := json.Marshal(map[string]any{
				"model": "test", "input": "Hello", "stream": stream,
				"tools":     reference["tools"],
				"reasoning": map[string]any{"effort": "high", "summary": "detailed"},
			})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(request)))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			check := func(response map[string]any) {
				t.Helper()
				for _, field := range []string{"temperature", "top_p", "top_k"} {
					if _, ok := response[field]; ok {
						t.Errorf("response includes removed sampling field %q", field)
					}
				}
				for key, want := range reference {
					got, ok := response[key]
					if key == "reasoning" && response["status"] == "in_progress" {
						// Effective context is unavailable before the provider responds.
						want = map[string]any{"effort": "high", "summary": "detailed", "mode": "standard", "context": nil}
					}
					if !ok || !reflect.DeepEqual(got, want) {
						t.Errorf("%s: got %#v (present %v), want %#v", key, got, ok, want)
					}
				}
			}
			if !stream {
				var response map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				check(response)
				return
			}
			events, err := harness.ParseSSE(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			checked := 0
			for _, event := range events {
				if response, ok := event.Data["response"].(map[string]any); ok {
					check(response)
					checked++
				}
			}
			if checked != 3 {
				t.Fatalf("checked %d response envelopes, want created, in_progress, completed", checked)
			}
		})
	}
}
