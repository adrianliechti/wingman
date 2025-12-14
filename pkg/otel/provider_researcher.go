package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/researcher"

	"go.opentelemetry.io/otel"
)

type Researcher interface {
	Observable
	researcher.Provider
}

type observableResearcher struct {
	model    string
	provider string

	researcher researcher.Provider
}

func NewResearcher(provider, model string, p researcher.Provider) Researcher {
	return &observableResearcher{
		researcher: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableResearcher) otelSetup() {
}

func (p *observableResearcher) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "research "+p.model)
	defer span.End()

	result, err := p.researcher.Research(ctx, instructions, options)

	return result, err
}
