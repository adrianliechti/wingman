package otel

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/semconv/v1.38.0/genaiconv"
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

func (p *observableEmbedder) Embed(ctx context.Context, texts []string) (*provider.Embedding, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "embeddings "+p.model)
	defer span.End()

	timestamp := time.Now()

	result, err := p.embedder.Embed(ctx, texts)

	if result != nil {
		duration := time.Since(timestamp).Seconds()

		providerName := genaiconv.ProviderNameAttr(p.provider)
		providerModel := p.model

		if result.Model != "" {
			providerModel = result.Model
		}

		p.operationDurationMetric.Record(ctx, duration,
			genaiconv.OperationNameEmbeddings,
			providerName,
			p.operationDurationMetric.AttrRequestModel(p.model),
			p.operationDurationMetric.AttrResponseModel(providerModel),
		)

		if result.Usage != nil {
			if result.Usage.InputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(result.Usage.InputTokens),
					genaiconv.OperationNameEmbeddings,
					providerName,
					genaiconv.TokenTypeInput,
					p.tokenUsageMetric.AttrRequestModel(p.model),
					p.tokenUsageMetric.AttrResponseModel(providerModel),
				)
			}

			if result.Usage.OutputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(result.Usage.OutputTokens),
					genaiconv.OperationNameEmbeddings,
					providerName,
					genaiconv.TokenTypeOutput,
					p.tokenUsageMetric.AttrRequestModel(p.model),
					p.tokenUsageMetric.AttrResponseModel(providerModel),
				)
			}
		}
	}

	return result, err
}
