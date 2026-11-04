package api_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/test/webtest"
)

func TestSearchExcerptsAndOptionalText(t *testing.T) {
	for _, backend := range []string{"exa", "tavily", "plain"} {
		for _, format := range []string{"form", "json"} {
			for _, include := range []string{"", "true", "false", "null", "{}", `{"unknown":true}`} {
				s, providers := webtest.NewServer(t, false)
				p := providers[backend]
				p.Caps.Content = backend != "plain"
				page := "Integrated page text"
				p.Results = []searcher.Result{{Source: "https://example.com", Excerpt: "Evidence"}}
				if p.Caps.Content {
					p.Results[0].Text = &page
				}
				form := url.Values{"model": {backend}, "query": {"test"}, "text": {include}}
				body, media := form.Encode(), "application/x-www-form-urlencoded"
				if format == "json" {
					request := map[string]any{"model": backend, "query": "test"}
					if include != "" {
						request["text"] = jsontext.Value(include)
					}
					data, err := json.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					body, media = string(data), "application/json"
				}
				r := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(body))
				r.Header.Set("Content-Type", media)
				r.Header.Set("Authorization", "Bearer wingman-test-key")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if include == `{"unknown":true}` {
					if w.Code != 400 || p.Searches != 0 || p.Scrapes != 0 {
						t.Fatal("invalid selector reached provider")
					}
					continue
				}
				if w.Code != 200 {
					t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
				}
				var hits []map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
					t.Fatal(err)
				}
				if len(hits) != 1 || hits[0]["excerpt"] != "Evidence" || p.Query != "test" {
					t.Fatal("excerpt lost")
				}
				wantFetches := 0
				wantText := include == "true" || include == "{}"
				if wantText {
					if !p.Caps.Content {
						page, wantFetches = "Grüezi 世界 — full page", 1
					}
					if hits[0]["text"] != page {
						t.Fatal("optional page text lost")
					}
				} else if _, ok := hits[0]["text"]; ok {
					t.Fatal("unrequested text included")
				}
				if p.Scrapes != wantFetches || (p.Options.Text != nil) != (wantText && p.Caps.Content) {
					t.Fatal("unnecessary fetching or wrong provider selection")
				}
			}
		}
	}
}

func TestSearchRejectsLegacyTextQueryAndInvalidOptions(t *testing.T) {
	for _, tc := range []struct{ media, body string }{
		{"application/x-www-form-urlencoded", "text=legacy+query"},
		{"application/json", `{"text":"legacy query"}`},
		{"application/json", `{"query":"test","text":"legacy query"}`},
		{"application/json", `{"text":{}}`},
		{"application/json", `{"query":"test","text":[]}`},
		{"application/x-www-form-urlencoded", "query=test&text=%7B%7D+%7B%7D"},
		{"application/json", `{"query":"test"} {}`},
		{"application/json", `{"query":"test","future_vendor_option":true}`},
	} {
		s, providers := webtest.NewServer(t, false)
		r := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.media)
		r.Header.Set("Authorization", "Bearer wingman-test-key")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 400 || providers["tavily"].Searches != 0 || providers["tavily"].Scrapes != 0 {
			t.Fatalf("invalid request reached provider: %s HTTP %d", tc.body, w.Code)
		}
	}
}

func TestJSONSearchPreservesFilters(t *testing.T) {
	s, providers := webtest.NewServer(t, false)
	w := webtest.Send(t, s, "/v1/search", `{"model":"exa","input":"test","text":{},"limit":2,"category":"news","location":"CH","domain":["example.com","!spam.example"],"since":"2026-10-01","until":"2026-10-02"}`, nil)
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	p := providers["exa"]
	o := p.Options
	if p.Query != "test" || *o.Limit != 2 || o.Category != "news" || o.Location != "CH" || len(o.Include) != 1 || o.Include[0] != "example.com" || len(o.Exclude) != 1 || o.Exclude[0] != "spam.example" || o.Since == nil || o.Until == nil || o.Until.Format("2006-01-02") != "2026-10-03" || p.Scrapes != 1 {
		t.Fatalf("JSON search lost query, filters, inclusive date or page text: %+v", o)
	}
}
