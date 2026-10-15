package guard

import (
	"github.com/adrianliechti/wingman/pkg/middleware"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

func SearcherMiddleware(g Provider) middleware.Middleware[searcher.Provider] {
	return func(next searcher.Provider) searcher.Provider {
		return NewSearcher(g, next)
	}
}

func ScraperMiddleware(g Provider) middleware.Middleware[scraper.Provider] {
	return func(next scraper.Provider) scraper.Provider {
		return NewScraper(g, next)
	}
}

func ResearcherMiddleware(g Provider) middleware.Middleware[researcher.Provider] {
	return func(next researcher.Provider) researcher.Provider {
		return NewResearcher(g, next)
	}
}
