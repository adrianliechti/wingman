package otel

import (
	"context"
	"iter"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
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

func (p *observableSynthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) iter.Seq2[*provider.Synthesis, error] {
	return func(yield func(*provider.Synthesis, error) bool) {
		ctx, span := otel.Tracer(instrumentationName).Start(ctx, GenAISpanName(genaiconv.OperationNameGenerateContent, p.model), trace.WithSpanKind(trace.SpanKindClient))
		defer span.End()

		if span.IsRecording() {
			span.SetAttributes(KeyValues(
				RequestAttrs(semconv.GenAIOperationNameGenerateContent, p.provider, p.model),
			)...)
		}

		timestamp := time.Now()
		providerName := genaiconv.ProviderNameAttr(p.provider)

		var providerModel string
		var lastErr error

		// Deferred so consumer cancellation still records the observation.
		defer func() {
			duration := time.Since(timestamp).Seconds()

			if providerModel == "" {
				providerModel = p.model
			} else if span.IsRecording() {
				span.SetAttributes(semconv.GenAIResponseModel(providerModel))
			}

			attrs := MetricAttrs(ctx, p.model, providerModel)

			if lastErr != nil {
				attrs = append(attrs, p.operationDurationMetric.AttrErrorType(ErrorTypeAttr(lastErr)))
			}

			p.operationDurationMetric.Record(ctx, duration,
				genaiconv.OperationNameGenerateContent,
				providerName,
				attrs...,
			)
		}()

		for result, err := range p.synthesizer.Synthesize(ctx, content, options) {
			if err != nil {
				lastErr = err
				RecordError(span, err)

				yield(nil, err)
				return
			}

			if result != nil && result.Model != "" {
				providerModel = result.Model
			}

			if !yield(result, nil) {
				return
			}
		}
	}
}
