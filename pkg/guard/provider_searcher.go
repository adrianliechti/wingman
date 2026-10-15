package guard

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

var _ searcher.Provider = (*guardedSearcher)(nil)

type guardedSearcher struct {
	searcher.Provider
	guard Provider
}

func NewSearcher(g Provider, next searcher.Provider) searcher.Provider {
	if g == nil {
		return next
	}
	return &guardedSearcher{Provider: next, guard: g}
}

func (p *guardedSearcher) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	if err := check(ctx, p.guard, query); err != nil {
		return nil, err
	}

	return p.Provider.Search(ctx, query, options)
}
