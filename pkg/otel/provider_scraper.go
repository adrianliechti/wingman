package otel

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/scraper"
	"go.opentelemetry.io/otel"
)

type Scraper interface {
	Observable
	scraper.Provider
}

type observableScraper struct {
	name    string
	library string

	provider string

	scraper scraper.Provider
}

func NewScraper(provider string, p scraper.Provider) Scraper {
	library := strings.ToLower(provider)

	return &observableScraper{
		scraper: p,

		name:    strings.TrimSuffix(strings.ToLower(provider), "-scraper") + "-scraper",
		library: library,

		provider: provider,
	}
}

func (p *observableScraper) otelSetup() {
}

func (p *observableScraper) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	ctx, span := otel.Tracer(p.library).Start(ctx, p.name)
	defer span.End()

	result, err := p.scraper.Scrape(ctx, url, options)

	return result, err
}
