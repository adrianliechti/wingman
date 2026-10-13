package proxy

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

var _ searcher.Provider = (*guardedSearcher)(nil)

type guardedSearcher struct {
	guard    guard.Provider
	searcher searcher.Provider
}

func NewSearcher(g guard.Provider, p searcher.Provider) searcher.Provider {
	if g == nil {
		return p
	}

	return &guardedSearcher{
		guard:    g,
		searcher: p,
	}
}

func (p *guardedSearcher) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	if err := check(ctx, p.guard, query); err != nil {
		return nil, err
	}

	return p.searcher.Search(ctx, query, options)
}

func (p *guardedSearcher) Capabilities() searcher.Capabilities {
	return p.searcher.Capabilities()
}
