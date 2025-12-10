package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/translator"

	"go.opentelemetry.io/otel"
)

type Translator interface {
	Observable
	translator.Provider
}

type observableTranslator struct {
	model    string
	provider string

	translator translator.Provider
}

func NewTranslator(provider, model string, p translator.Provider) Translator {
	return &observableTranslator{
		translator: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableTranslator) otelSetup() {
}

func (p *observableTranslator) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "translate "+p.model)
	defer span.End()

	result, err := p.translator.Translate(ctx, input, options)

	return result, err
}
