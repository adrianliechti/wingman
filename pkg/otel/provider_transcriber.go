package otel

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/semconv/v1.38.0/genaiconv"
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

func (p *observableTranscriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) (*provider.Transcription, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "transcribe "+p.model)
	defer span.End()

	timestamp := time.Now()

	result, err := p.transcriber.Transcribe(ctx, input, options)

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
