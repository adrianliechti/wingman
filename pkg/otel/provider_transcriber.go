package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
)

type Transcriber interface {
	Observable
	provider.Transcriber
}

type observableTranscriber struct {
	model    string
	provider string

	transcriber provider.Transcriber
}

func NewTranscriber(provider, model string, p provider.Transcriber) Transcriber {
	return &observableTranscriber{
		transcriber: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableTranscriber) otelSetup() {
}

func (p *observableTranscriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) (*provider.Transcription, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "transcribe "+p.model)
	defer span.End()

	result, err := p.transcriber.Transcribe(ctx, input, options)

	return result, err
}
