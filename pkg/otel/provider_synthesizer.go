package otel

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
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
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, GenAISpanName(genaiconv.OperationNameGenerateContent, p.model), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	if span.IsRecording() {
		span.SetAttributes(KeyValues(
			RequestAttrs(semconv.GenAIOperationNameGenerateContent, p.provider, p.model),
			EndUserAttrs(ctx),
		)...)
	}

	timestamp := time.Now()

	result, err := p.synthesizer.Synthesize(ctx, content, options)

	if err != nil {
		RecordError(span, err)
	}

	duration := time.Since(timestamp).Seconds()
	providerName := genaiconv.ProviderNameAttr(p.provider)
	providerModel := p.model

	if result != nil {
		if result.Model != "" {
			providerModel = result.Model
		}

		if span.IsRecording() {
			span.SetAttributes(semconv.GenAIResponseModel(providerModel))
		}
	}

	attrs := []KeyValue{
		semconv.GenAIRequestModel(p.model),
		semconv.GenAIResponseModel(providerModel),
	}

	if err != nil {
		attrs = append(attrs, p.operationDurationMetric.AttrErrorType(ErrorTypeAttr(err)))
	}

	p.operationDurationMetric.Record(ctx, duration,
		genaiconv.OperationNameGenerateContent,
		providerName,
		attrs...,
	)

	return result, err
}
