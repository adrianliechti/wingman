package scraper

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/guard"
)

var _ Provider = (*guardedScraper)(nil)

type guardedScraper struct {
	guard   guard.Provider
	scraper Provider
}

// NewGuard checks input before forwarding requests to the provider.
func NewGuard(g guard.Provider, next Provider) Provider {
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
