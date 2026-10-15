package otel

import (
	"github.com/adrianliechti/wingman/pkg/middleware"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

func SearcherMiddleware(provider, model string) middleware.Middleware[searcher.Provider] {
	return func(next searcher.Provider) searcher.Provider {
		return NewSearcher(provider, model, next)
	}
}

func ScraperMiddleware(provider, model string) middleware.Middleware[scraper.Provider] {
	return func(next scraper.Provider) scraper.Provider {
		return NewScraper(provider, model, next)
	}
}

func ResearcherMiddleware(provider, model string) middleware.Middleware[researcher.Provider] {
	return func(next researcher.Provider) researcher.Provider {
		return NewResearcher(provider, model, next)
	}
}
