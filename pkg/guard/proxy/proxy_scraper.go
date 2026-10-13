package proxy

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/scraper"
)

var _ scraper.Provider = (*guardedScraper)(nil)

type guardedScraper struct {
	guard   guard.Provider
	scraper scraper.Provider
}

func NewScraper(g guard.Provider, p scraper.Provider) scraper.Provider {
	if g == nil {
		return p
	}

	return &guardedScraper{
		guard:   g,
		scraper: p,
	}
}

func (p *guardedScraper) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	if err := check(ctx, p.guard, url); err != nil {
		return nil, err
	}

	return p.scraper.Scrape(ctx, url, options)
}
