package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
)

type Synthesizer interface {
	Observable
	provider.Synthesizer
}

type observableSynthesizer struct {
	model    string
	provider string

	synthesizer provider.Synthesizer
}

func NewSynthesizer(provider, model string, p provider.Synthesizer) Synthesizer {
	return &observableSynthesizer{
		synthesizer: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableSynthesizer) otelSetup() {
}

func (p *observableSynthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) (*provider.Synthesis, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "synthesize "+p.model)
	defer span.End()

	result, err := p.synthesizer.Synthesize(ctx, content, options)

	return result, err
}
