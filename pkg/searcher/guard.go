package searcher

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/guard"
)

var _ Provider = (*guardedSearcher)(nil)

type guardedSearcher struct {
	Provider
	guard guard.Provider
}

func WithGuard(g guard.Provider, next Provider) Provider {
	if g == nil {
		return next
	}
	return &guardedSearcher{guard: g, Provider: next}
}

func (p *guardedSearcher) Search(ctx context.Context, query string, options *SearchOptions) ([]Result, error) {
	if err := guard.Check(ctx, p.guard, query); err != nil {
		return nil, err
	}

	return p.Provider.Search(ctx, query, options)
}
