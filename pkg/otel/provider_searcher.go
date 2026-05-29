package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/searcher"

	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
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
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, string(genaiconv.OperationNameRetrieval), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	if span.IsRecording() {
		attrs := []KeyValue{
			semconv.GenAIOperationNameRetrieval,
		}
		if p.provider != "" {
			attrs = append(attrs, semconv.GenAIProviderNameKey.String(p.provider))
		}
		span.SetAttributes(KeyValues(attrs, EndUserAttrs(ctx))...)
	}

	result, err := p.searcher.Search(ctx, query, options)

	if err != nil {
		RecordError(span, err)
	}

	return result, err
}
