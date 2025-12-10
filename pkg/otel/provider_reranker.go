package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
)

type Reranker interface {
	Observable
	provider.Reranker
}

type observableReranker struct {
	model    string
	provider string

	reranker provider.Reranker
}

func NewReranker(provider, model string, p provider.Reranker) Reranker {
	return &observableReranker{
		reranker: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableReranker) otelSetup() {
}

func (p *observableReranker) Rerank(ctx context.Context, query string, inputs []string, options *provider.RerankOptions) ([]provider.Ranking, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "rerank "+p.model)
	defer span.End()

	result, err := p.reranker.Rerank(ctx, query, inputs, options)

	return result, err
}
