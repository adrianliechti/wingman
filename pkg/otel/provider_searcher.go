package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/searcher"

	"go.opentelemetry.io/otel"
)

type Searcher interface {
	Observable
	searcher.Provider
}

type observableSearcher struct {
	model    string
	provider string

	searcher searcher.Provider
}

func NewSearcher(provider, model string, p searcher.Provider) Searcher {
	return &observableSearcher{
		searcher: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableSearcher) otelSetup() {
}

func (p *observableSearcher) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "search "+p.model)
	defer span.End()

	result, err := p.searcher.Search(ctx, query, options)

	return result, err
}
