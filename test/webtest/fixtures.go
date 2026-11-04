package webtest

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/static"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/server"
)

type TestPolicy struct {
	Denied bool `json:"denied"`
}

func (p TestPolicy) Verify(context.Context, policy.Resource, string, policy.Action) error {
	if p.Denied {
		return policy.ErrAccessDenied
	}
	return nil
}

type Provider struct {
	mu       sync.Mutex
	Options  searcher.SearchOptions `json:"options"`
	Query    string                 `json:"query"`
	Searches int                    `json:"searches"`
	Scrapes  int                    `json:"scrapes"`
	Err      error                  `json:"err"`
	Score    *float64               `json:"score"`
	Caps     searcher.Capabilities  `json:"caps"`
	Results  []searcher.Result      `json:"results"`
}

func (p *Provider) SearchCapabilities() searcher.Capabilities { return p.Caps }
func (p *Provider) Search(_ context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Searches++
	p.Options = *options
	p.Query = query
	if p.Results != nil {
		return p.Results, p.Err
	}
	published := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return []searcher.Result{{Source: "https://example.com", Title: "Example", Excerpt: "Evidence", Timestamp: &published, Score: p.Score}}, p.Err
}
func (p *Provider) Scrape(ctx context.Context, url string, _ *scraper.ScrapeOptions) (*scraper.Document, error) {
	p.mu.Lock()
	p.Scrapes++
	p.mu.Unlock()
	if strings.HasSuffix(url, "/missing") {
		return nil, errors.New("page unavailable")
	}
	if strings.HasSuffix(url, "/slow") {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.Err != nil {
		return nil, p.Err
	}
	return &scraper.Document{Text: "Grüezi 世界 — full page"}, nil
}

func NewServer(t *testing.T, denied bool) (*server.Server, map[string]*Provider) {
	t.Helper()
	a, _ := static.New("wingman-test-key")
	cfg := &config.Config{Policy: TestPolicy{Denied: denied}, Authorizers: []auth.Provider{a}}
	score := 0.0
	providers := map[string]*Provider{
		"tavily": {Caps: searcher.Capabilities{DateFilters: true, Categories: []searcher.Category{{Name: "general"}, {Name: "news"}, {Name: "finance"}}}, Score: &score},
		"exa":    {Caps: searcher.Capabilities{DateFilters: true, Categories: []searcher.Category{{Name: "news"}, {Name: "financial report"}}}},
		"plain":  {},
	}
	for _, name := range []string{"tavily", "exa", "plain"} {
		p := providers[name]
		cfg.RegisterSearcher(name, otel.NewSearcher(name, name, p))
		cfg.RegisterScraper(name, otel.NewScraper(name, name, p))
	}
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, providers
}

func Send(t *testing.T, s http.Handler, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if headers == nil {
		headers = map[string]string{"Authorization": "Bearer wingman-test-key"}
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func Response(t *testing.T, w *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP %d (want %d): %s", w.Code, status, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("non-JSON Response")
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
