package otel

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

type stubSearcher struct{}

func (stubSearcher) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	return nil, nil
}

func (stubSearcher) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{}
}

type stubScraper struct{}

func (stubScraper) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	return nil, nil
}

type stubResearcher struct{}

func (stubResearcher) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return func(yield func(*researcher.Result, error) bool) {}
}

type stubGuard struct{}

func (stubGuard) Check(ctx context.Context, text string, options *guard.CheckOptions) (*guard.Result, error) {
	return &guard.Result{}, nil
}

// Instrumented must see through middleware so wiring code does not wrap an
// already observable provider a second time.
func TestInstrumented(t *testing.T) {
	plain := searcher.Provider(stubSearcher{})
	observable := searcher.Provider(NewSearcher("stub", "stub", plain))

	tests := map[string]struct {
		provider searcher.Provider
		want     bool
	}{
		"plain":              {plain, false},
		"observable":         {observable, true},
		"guarded plain":      {searcher.WithGuard(stubGuard{}, plain), false},
		"guarded observable": {searcher.WithGuard(stubGuard{}, observable), true},
	}

	for name, test := range tests {
		if got := Instrumented(test.provider); got != test.want {
			t.Errorf("%s: Instrumented() = %v, want %v", name, got, test.want)
		}
	}
}

func TestInstrumentedScraper(t *testing.T) {
	plain := scraper.Provider(stubScraper{})

	if Instrumented(plain) {
		t.Error("plain scraper reported as instrumented")
	}

	guarded := scraper.WithGuard(stubGuard{}, NewScraper("stub", "stub", plain))

	if !Instrumented(guarded) {
		t.Error("guarded observable scraper reported as not instrumented")
	}
}

func TestInstrumentedResearcher(t *testing.T) {
	plain := researcher.Provider(stubResearcher{})

	if Instrumented(plain) {
		t.Error("plain researcher reported as instrumented")
	}

	guarded := researcher.WithGuard(stubGuard{}, NewResearcher("stub", "stub", plain))

	if !Instrumented(guarded) {
		t.Error("guarded observable researcher reported as not instrumented")
	}
}
