package guard

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/scraper"
)

var _ scraper.Provider = (*guardedScraper)(nil)

type guardedScraper struct {
	guard   Provider
	scraper scraper.Provider
}

func NewScraper(g Provider, next scraper.Provider) scraper.Provider {
	if g == nil {
		return next
	}
	return &guardedScraper{guard: g, scraper: next}
}

func (p *guardedScraper) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	if err := check(ctx, p.guard, url); err != nil {
		return nil, err
	}

	return p.scraper.Scrape(ctx, url, options)
}
