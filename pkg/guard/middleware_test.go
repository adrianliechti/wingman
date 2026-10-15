package guard_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

type stubGuard struct {
	err      error
	flagged  bool
	empty    bool
	requests []string
}

func (g *stubGuard) Check(ctx context.Context, text string, options *guard.CheckOptions) (*guard.Result, error) {
	g.requests = append(g.requests, text)

	if g.err != nil {
		return nil, g.err
	}
	if g.empty {
		return nil, nil
	}

	if !g.flagged {
		return &guard.Result{}, nil
	}

	return &guard.Result{
		Flagged: true,

		Categories: []guard.Category{
			{Name: "test", Score: 0.5},
		},
	}, nil
}

type stubScraper struct {
	called bool
}

func (p *stubScraper) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	p.called = true
	return &scraper.Document{Text: "content"}, nil
}

type stubSearcher struct {
	called bool
}

func (p *stubSearcher) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	p.called = true
	return []searcher.Result{{Source: "https://example.com"}}, nil
}

func (p *stubSearcher) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{DateFilters: true}
}

type stubResearcher struct {
	called bool
}

func (p *stubResearcher) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return func(yield func(*researcher.Result, error) bool) {
		p.called = true
		yield(&researcher.Result{Content: "report"}, nil)
	}
}

func TestMiddlewareChecksBeforeForwarding(t *testing.T) {
	g := &stubGuard{flagged: true}

	scrapeProvider := &stubScraper{}
	searchProvider := &stubSearcher{}
	researchProvider := &stubResearcher{}

	if _, err := guard.ScraperMiddleware(g)(scrapeProvider).Scrape(context.Background(), "https://example.com/blocked", nil); !isFlagged(err) {
		t.Fatalf("scrape error = %v", err)
	}

	if _, err := guard.SearcherMiddleware(g)(searchProvider).Search(context.Background(), "blocked query", nil); !isFlagged(err) {
		t.Fatalf("search error = %v", err)
	}

	_, err := researcher.Collect(guard.ResearcherMiddleware(g)(researchProvider).Research(context.Background(), "blocked instructions", nil))

	if !isFlagged(err) {
		t.Fatalf("research error = %v", err)
	}

	if scrapeProvider.called || searchProvider.called || researchProvider.called {
		t.Fatal("provider called despite flagged content")
	}

	want := []string{"https://example.com/blocked", "blocked query", "blocked instructions"}

	if len(g.requests) != len(want) {
		t.Fatalf("guard requests = %v, want %v", g.requests, want)
	}

	for i, text := range want {
		if g.requests[i] != text {
			t.Fatalf("guard request[%d] = %q, want %q", i, g.requests[i], text)
		}
	}
}

func TestMiddlewareForwardsAllowedRequests(t *testing.T) {
	g := &stubGuard{}

	scrapeProvider := &stubScraper{}
	searchProvider := &stubSearcher{}
	researchProvider := &stubResearcher{}

	if _, err := guard.ScraperMiddleware(g)(scrapeProvider).Scrape(context.Background(), "https://example.com", nil); err != nil {
		t.Fatal(err)
	}

	guardedSearcher := guard.SearcherMiddleware(g)(searchProvider)

	if _, err := guardedSearcher.Search(context.Background(), "query", nil); err != nil {
		t.Fatal(err)
	}

	if !guardedSearcher.Capabilities().DateFilters {
		t.Fatal("capabilities not forwarded")
	}

	result, err := researcher.Collect(guard.ResearcherMiddleware(g)(researchProvider).Research(context.Background(), "instructions", nil))

	if err != nil {
		t.Fatal(err)
	}

	if result.Content != "report" {
		t.Fatalf("content = %q", result.Content)
	}

	if !scrapeProvider.called || !searchProvider.called || !researchProvider.called {
		t.Fatal("provider not called")
	}
}

func TestMiddlewarePropagatesGuardErrors(t *testing.T) {
	sentinel := errors.New("guard unavailable")
	for _, tc := range []struct {
		name  string
		guard *stubGuard
	}{
		{"unavailable", &stubGuard{err: sentinel}},
		{"empty response", &stubGuard{empty: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scrapeProvider := &stubScraper{}
			searchProvider := &stubSearcher{}
			researchProvider := &stubResearcher{}
			_, scrapeErr := guard.ScraperMiddleware(tc.guard)(scrapeProvider).Scrape(context.Background(), "https://example.com", nil)
			_, searchErr := guard.SearcherMiddleware(tc.guard)(searchProvider).Search(context.Background(), "query", nil)
			_, researchErr := researcher.Collect(guard.ResearcherMiddleware(tc.guard)(researchProvider).Research(context.Background(), "instructions", nil))
			for _, err := range []error{scrapeErr, searchErr, researchErr} {
				if err == nil || (tc.guard.err != nil && !errors.Is(err, sentinel)) {
					t.Fatalf("error = %v", err)
				}
			}
			if scrapeProvider.called || searchProvider.called || researchProvider.called {
				t.Fatal("provider called without a guard decision")
			}
		})
	}
}

func TestMiddlewareChecksBlankInput(t *testing.T) {
	for _, input := range []string{"", " \t\n"} {
		t.Run(input, func(t *testing.T) {
			g := &stubGuard{flagged: true}
			p := &stubSearcher{}
			if _, err := guard.SearcherMiddleware(g)(p).Search(context.Background(), input, nil); !isFlagged(err) {
				t.Fatalf("error = %v", err)
			}
			if p.called || len(g.requests) != 1 || g.requests[0] != input {
				t.Fatal("blank input bypassed the guard")
			}
		})
	}
}

func TestMiddlewareWithoutGuardReturnsProvider(t *testing.T) {
	scrapeProvider := &stubScraper{}
	searchProvider := &stubSearcher{}
	researchProvider := &stubResearcher{}

	if guard.ScraperMiddleware(nil)(scrapeProvider) != scraper.Provider(scrapeProvider) {
		t.Fatal("scraper wrapped without guard")
	}

	if guard.SearcherMiddleware(nil)(searchProvider) != searcher.Provider(searchProvider) {
		t.Fatal("searcher wrapped without guard")
	}

	if guard.ResearcherMiddleware(nil)(researchProvider) != researcher.Provider(researchProvider) {
		t.Fatal("researcher wrapped without guard")
	}
}

type researcherFunc func(context.Context, string, *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error]

func (f researcherFunc) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return f(ctx, instructions, options)
}

func TestResearcherPreservesStream(t *testing.T) {
	for _, stopEarly := range []bool{false, true} {
		name := "complete"
		if stopEarly {
			name = "stop early"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			options := &researcher.ResearchOptions{}
			progress := &researcher.Result{Progress: "working"}
			report := &researcher.Result{Content: "report"}
			sentinel := errors.New("upstream error")
			g := &stubGuard{}
			called, closed := false, false
			upstream := researcherFunc(func(gotCtx context.Context, input string, gotOptions *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
				called = true
				if gotCtx != ctx || input != "instructions" || gotOptions != options {
					t.Fatal("research arguments changed")
				}
				if len(g.requests) != 1 {
					t.Fatal("research started before the guard check")
				}
				return func(yield func(*researcher.Result, error) bool) {
					defer func() { closed = true }()
					if !yield(progress, nil) || !yield(report, nil) {
						return
					}
					yield(nil, sentinel)
				}
			})
			stream := guard.ResearcherMiddleware(g)(upstream).Research(ctx, "instructions", options)
			if called || len(g.requests) != 0 {
				t.Fatal("research was not lazy")
			}
			count := 0
			for result, err := range stream {
				want := []*researcher.Result{progress, report, nil}[count]
				if result != want || (count < 2 && err != nil) || (count == 2 && !errors.Is(err, sentinel)) {
					t.Fatalf("item %d = (%v, %v)", count, result, err)
				}
				count++
				if stopEarly {
					break
				}
			}
			wantCount := 3
			if stopEarly {
				wantCount = 1
			}
			if count != wantCount || !closed {
				t.Fatalf("items = %d, closed = %v", count, closed)
			}
		})
	}
}

func isFlagged(err error) bool {
	var flagged *guard.FlaggedError
	return errors.As(err, &flagged) && len(flagged.Categories) == 1 && flagged.Categories[0].Name == "test"
}
