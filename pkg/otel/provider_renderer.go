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

type Renderer interface {
	Observable
	provider.Renderer
}

type observableRenderer struct {
	model    string
	provider string

	renderer provider.Renderer

	operationDurationMetric genaiconv.ClientOperationDuration
}

func NewRenderer(provider, model string, p provider.Renderer) Renderer {
	meter := otel.Meter(instrumentationName)

	operationDurationMetric, _ := genaiconv.NewClientOperationDuration(meter)

	return &observableRenderer{
		renderer: p,

		model:    model,
		provider: provider,

		operationDurationMetric: operationDurationMetric,
	}
}

func (p *observableRenderer) otelSetup() {
}

func (p *observableRenderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, GenAISpanName(genaiconv.OperationNameGenerateContent, p.model), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	if span.IsRecording() {
		span.SetAttributes(KeyValues(
			RequestAttrs(semconv.GenAIOperationNameGenerateContent, p.provider, p.model),
			EndUserAttrs(ctx),
		)...)
	}

	timestamp := time.Now()

	result, err := p.renderer.Render(ctx, input, options)

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
