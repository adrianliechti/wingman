package searcher

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/guard"
)

var _ Provider = (*guardedSearcher)(nil)

type guardedSearcher struct {
	searcher Provider
	guard    guard.Provider
}

func WithGuard(g guard.Provider, next Provider) Provider {
	if g == nil {
		return next
	}
	return &guardedSearcher{guard: g, searcher: next}
}

func (p *guardedSearcher) Search(ctx context.Context, query string, options *SearchOptions) ([]Result, error) {
	if err := guard.Check(ctx, p.guard, query); err != nil {
		return nil, err
	}

	return p.searcher.Search(ctx, query, options)
}

func (p *guardedSearcher) Capabilities() Capabilities {
	return p.searcher.Capabilities()
}

func (p *guardedSearcher) Unwrap() Provider {
	return p.searcher
}
