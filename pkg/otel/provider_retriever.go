package otel

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/retriever"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Retriever interface {
	Observable
	retriever.Provider
}

type observableRetriever struct {
	name    string
	library string

	provider string

	retriever retriever.Provider
}

func NewRetriever(provider, index string, p retriever.Provider) Retriever {
	library := strings.ToLower(provider)

	return &observableRetriever{
		retriever: p,

		name:    strings.TrimSuffix(strings.ToLower(provider), "-retriever") + "-retriever",
		library: library,

		provider: provider,
	}
}

func (p *observableRetriever) otelSetup() {
}

func (p *observableRetriever) Retrieve(ctx context.Context, query string, options *retriever.RetrieveOptions) ([]retriever.Result, error) {
	ctx, span := otel.Tracer(p.library).Start(ctx, p.name)
	defer span.End()

	result, err := p.retriever.Retrieve(ctx, query, options)

	if EnableDebug {
		span.SetAttributes(attribute.String("query", query))

		if result != nil {
			var outputs []string

			for _, r := range result {
				outputs = append(outputs, r.Content)
			}

			if len(outputs) > 0 {
				span.SetAttributes(attribute.StringSlice("results", outputs))
			}
		}
	}

	return result, err
}
