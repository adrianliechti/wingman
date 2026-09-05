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

type Transcriber interface {
	Observable
	provider.Transcriber
}

type observableTranscriber struct {
	model    string
	provider string

	transcriber provider.Transcriber

	operationDurationMetric genaiconv.ClientOperationDuration
}

func NewTranscriber(provider, model string, p provider.Transcriber) Transcriber {
	meter := otel.Meter(instrumentationName)

	operationDurationMetric, _ := genaiconv.NewClientOperationDuration(meter)

	return &observableTranscriber{
		transcriber: p,

		model:    model,
		provider: provider,

		operationDurationMetric: operationDurationMetric,
	}
}

func (p *observableTranscriber) otelSetup() {
}

func (p *observableTranscriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) iter.Seq2[*provider.Transcription, error] {
	return func(yield func(*provider.Transcription, error) bool) {
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

		for result, err := range p.transcriber.Transcribe(ctx, input, options) {
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
