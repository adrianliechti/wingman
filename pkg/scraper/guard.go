package scraper

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/guard"
)

var _ Provider = (*guardedScraper)(nil)

type guardedScraper struct {
	scraper Provider
	guard   guard.Provider
}

func WithGuard(g guard.Provider, next Provider) Provider {
	if g == nil {
		return next
	}
	return &guardedScraper{guard: g, scraper: next}
}

func (p *guardedScraper) Scrape(ctx context.Context, url string, options *ScrapeOptions) (*Document, error) {
	if err := guard.Check(ctx, p.guard, url); err != nil {
		return nil, err
	}

	return p.scraper.Scrape(ctx, url, options)
}
