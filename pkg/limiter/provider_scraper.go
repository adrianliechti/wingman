package limiter

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/scraper"

	"golang.org/x/time/rate"
)

type Scraper interface {
	Limiter
	scraper.Provider
}

type limitedScraper struct {
	limiter  *rate.Limiter
	provider scraper.Provider
}

func NewScraper(l *rate.Limiter, p scraper.Provider) Scraper {
	return &limitedScraper{
		limiter:  l,
		provider: p,
	}
}

func (p *limitedScraper) limiterSetup() {
}

func (p *limitedScraper) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	if p.limiter != nil {
		p.limiter.Wait(ctx)
	}

	return p.provider.Scrape(ctx, url, options)
}
