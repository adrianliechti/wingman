package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/summarizer"

	"go.opentelemetry.io/otel"
)

type Summarizer interface {
	Observable
	summarizer.Provider
}

type observableSummarizer struct {
	model    string
	provider string

	summarizer summarizer.Provider
}

func NewSummarizer(provider, model string, p summarizer.Provider) Summarizer {
	return &observableSummarizer{
		summarizer: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableSummarizer) otelSetup() {
}

func (p *observableSummarizer) Summarize(ctx context.Context, text string, options *summarizer.SummarizerOptions) (*summarizer.Summary, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "summarize "+p.model)
	defer span.End()

	result, err := p.summarizer.Summarize(ctx, text, options)

	return result, err
}
