package exa_test

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/searcher/exa"
	"github.com/adrianliechti/wingman/test/webtest"
)

type exaTransport func(*http.Request) (*http.Response, error)

func (f exaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCurrentExaProviderThroughHandler(t *testing.T) {
	for _, mode := range []string{"auto", "lite"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := webtest.NewServer(t, false)
			calls := 0
			client := &http.Client{Transport: exaTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var body exa.SearchRequest
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				if body.Type != mode || body.Category != "company" || len(body.ExcludeDomains) != 1 || body.ExcludeDomains[0] != "spam.example" {
					t.Fatalf("lost provider mode or supported filters: %+v", body)
				}
				if body.Contents == nil || !body.Contents.Text || body.Contents.Highlights == nil || !body.Contents.Highlights.Dynamic || r.Header.Get("Exa-Beta") != "dynamic-highlights-2026-08-28" {
					t.Fatal("text and dynamic highlights were not requested")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"id":"https://example.com","url":"https://example.com","title":"Example","highlights":["Selected evidence"],"text":"Complete page"},{"url":"https://example.com/redundant","highlights":[],"text":"Redundant page"}]}`))}, nil
			})}
			p, err := exa.New("test", exa.WithClient(client), exa.WithMode(mode))
			if err != nil {
				t.Fatal(err)
			}
			s.RegisterSearcher("current-exa", p)
			body := `{"model":"current-exa","query":"software companies","category":"company","excludeDomains":["spam.example"],"type":"lite","contents":{"text":true,"highlights":{"dynamic":true}}}`
			got := webtest.Response(t, webtest.Send(t, s, "/v1/exa/search", body, map[string]string{"x-api-key": "wingman-test-key", "Exa-Beta": "dynamic-highlights-2026-08-28"}), http.StatusOK)
			hits := got["results"].([]any)
			if calls != 1 || len(hits) != 2 {
				t.Fatalf("search calls=%d results=%d", calls, len(hits))
			}
			first, second := hits[0].(map[string]any), hits[1].(map[string]any)
			if first["text"] != "Complete page" || first["highlights"].([]any)[0] != "Selected evidence" || second["text"] != "Redundant page" || second["highlights"] != nil {
				t.Fatalf("native evidence was changed: %#v", hits)
			}
		})
	}
}
