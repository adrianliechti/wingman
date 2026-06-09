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

type Embedder interface {
	Observable
	provider.Embedder
}

type observableEmbedder struct {
	model    string
	provider string

	embedder provider.Embedder

	tokenUsageMetric        genaiconv.ClientTokenUsage
	operationDurationMetric genaiconv.ClientOperationDuration
}

func NewEmbedder(provider, model string, p provider.Embedder) Embedder {
	meter := otel.Meter(instrumentationName)

	tokenUsageMetric, _ := genaiconv.NewClientTokenUsage(meter)
	operationDurationMetric, _ := genaiconv.NewClientOperationDuration(meter)

	return &observableEmbedder{
		embedder: p,

		model:    model,
		provider: provider,

		tokenUsageMetric:        tokenUsageMetric,
		operationDurationMetric: operationDurationMetric,
	}
}

func (p *observableEmbedder) otelSetup() {
}

func (p *observableEmbedder) Embed(ctx context.Context, texts []string, options *provider.EmbedOptions) (*provider.Embedding, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, GenAISpanName(genaiconv.OperationNameEmbeddings, p.model), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	if span.IsRecording() {
		attrs := RequestAttrs(semconv.GenAIOperationNameEmbeddings, p.provider, p.model)
		if options != nil && options.Dimensions != nil {
			attrs = append(attrs, semconv.GenAIEmbeddingsDimensionCount(*options.Dimensions))
		}
		span.SetAttributes(attrs...)
	}

	timestamp := time.Now()

	result, err := p.embedder.Embed(ctx, texts, options)

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
			span.SetAttributes(KeyValues(
				[]KeyValue{semconv.GenAIResponseModel(providerModel)},
				UsageAttrs(result.Usage),
			)...)
		}

		if result.Usage != nil {
			attrs := MetricAttrs(ctx, p.model, providerModel)

			if result.Usage.InputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(result.Usage.InputTokens),
					genaiconv.OperationNameEmbeddings,
					providerName,
					genaiconv.TokenTypeInput,
					attrs...,
				)
			}

			if result.Usage.OutputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(result.Usage.OutputTokens),
					genaiconv.OperationNameEmbeddings,
					providerName,
					genaiconv.TokenTypeOutput,
					attrs...,
				)
			}
		}
	}

	attrs := MetricAttrs(ctx, p.model, providerModel)

	if err != nil {
		attrs = append(attrs, p.operationDurationMetric.AttrErrorType(ErrorTypeAttr(err)))
	}

	p.operationDurationMetric.Record(ctx, duration,
		genaiconv.OperationNameEmbeddings,
		providerName,
		attrs...,
	)

	return result, err
}
