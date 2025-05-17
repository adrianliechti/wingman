package otel

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/translator"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Translator interface {
	Observable
	translator.Provider
}

type observableTranslator struct {
	name    string
	library string

	model    string
	provider string

	translator translator.Provider
}

func NewTranslator(provider, model string, p translator.Provider) Translator {
	library := strings.ToLower(provider)

	return &observableTranslator{
		translator: p,

		name:    strings.TrimSuffix(strings.ToLower(provider), "-translator") + "-translator",
		library: library,

		model:    model,
		provider: provider,
	}
}

func (p *observableTranslator) otelSetup() {
}

func (p *observableTranslator) Translate(ctx context.Context, content string, options *translator.TranslateOptions) (*translator.Translation, error) {
	ctx, span := otel.Tracer(p.library).Start(ctx, p.name)
	defer span.End()

	result, err := p.translator.Translate(ctx, content, options)

	meterRequest(ctx, p.library, p.provider, "translate", p.model)

	if EnableDebug {
		span.SetAttributes(attribute.String("input", content))

		if result != nil {
			if result.Text != "" {
				span.SetAttributes(attribute.String("output", result.Text))
			}
		}
	}

	return result, err
}
