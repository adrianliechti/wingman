package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/extractor"

	"go.opentelemetry.io/otel"
)

type Extractor interface {
	Observable
	extractor.Provider
}

type observableExtractor struct {
	model    string
	provider string

	extractor extractor.Provider
}

func NewExtractor(provider, model string, p extractor.Provider) Extractor {
	return &observableExtractor{
		extractor: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableExtractor) otelSetup() {
}

func (p *observableExtractor) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "extract "+p.model)
	defer span.End()

	result, err := p.extractor.Extract(ctx, file, options)

	return result, err
}
