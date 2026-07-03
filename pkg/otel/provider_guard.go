package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/guard"

	"go.opentelemetry.io/otel"
)

type Guard interface {
	Observable
	guard.Provider
}

type observableGuard struct {
	model    string
	provider string

	guard guard.Provider
}

func NewGuard(provider, model string, p guard.Provider) Guard {
	return &observableGuard{
		guard: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableGuard) otelSetup() {
}

func (p *observableGuard) Check(ctx context.Context, text string, options *guard.CheckOptions) (*guard.Result, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "guard "+p.model)
	defer span.End()

	result, err := p.guard.Check(ctx, text, options)

	if err != nil {
		RecordError(span, err)
	}

	return result, err
}
