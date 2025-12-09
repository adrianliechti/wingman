package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/scraper"

	"go.opentelemetry.io/otel"
)

type Scraper interface {
	Observable
	scraper.Provider
}

type observableScraper struct {
	model    string
	provider string

	scraper scraper.Provider
}

func NewScraper(provider, model string, p scraper.Provider) Scraper {
	return &observableScraper{
		scraper: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableScraper) otelSetup() {
}

func (p *observableScraper) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "scrape "+p.model)
	defer span.End()

	result, err := p.scraper.Scrape(ctx, url, options)

	return result, err
}
