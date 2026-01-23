package otel

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/semconv/v1.38.0/genaiconv"
)

type Synthesizer interface {
	Observable
	provider.Synthesizer
}

type observableSynthesizer struct {
	model    string
	provider string

	synthesizer provider.Synthesizer

	operationDurationMetric genaiconv.ClientOperationDuration
}

func NewSynthesizer(provider, model string, p provider.Synthesizer) Synthesizer {
	meter := otel.Meter(instrumentationName)

	operationDurationMetric, _ := genaiconv.NewClientOperationDuration(meter)

	return &observableSynthesizer{
		synthesizer: p,

		model:    model,
		provider: provider,

		operationDurationMetric: operationDurationMetric,
	}
}

func (p *observableSynthesizer) otelSetup() {
}

func (p *observableSynthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) (*provider.Synthesis, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "synthesize "+p.model)
	defer span.End()

	timestamp := time.Now()

	result, err := p.synthesizer.Synthesize(ctx, content, options)

	if result != nil {
		duration := time.Since(timestamp).Seconds()

		providerName := genaiconv.ProviderNameAttr(p.provider)
		providerModel := p.model

		if result.Model != "" {
			providerModel = result.Model
		}

		p.operationDurationMetric.Record(ctx, duration,
			genaiconv.OperationNameGenerateContent,
			providerName,
			KeyValues([]KeyValue{
				p.operationDurationMetric.AttrRequestModel(p.model),
				p.operationDurationMetric.AttrResponseModel(providerModel),
			}, EndUserAttrs(ctx))...,
		)
	}

	return result, err
}
