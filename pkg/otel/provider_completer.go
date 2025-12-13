package otel

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/semconv/v1.38.0/genaiconv"
)

type Completer interface {
	Observable
	provider.Completer
}

type observableCompleter struct {
	model    string
	provider string

	completer provider.Completer

	tokenUsageMetric        genaiconv.ClientTokenUsage
	operationDurationMetric genaiconv.ClientOperationDuration
}

func NewCompleter(provider, model string, p provider.Completer) Completer {
	meter := otel.Meter(instrumentationName)

	tokenUsageMetric, _ := genaiconv.NewClientTokenUsage(meter)
	operationDurationMetric, _ := genaiconv.NewClientOperationDuration(meter)

	return &observableCompleter{
		completer: p,

		model:    model,
		provider: provider,

		tokenUsageMetric:        tokenUsageMetric,
		operationDurationMetric: operationDurationMetric,
	}
}

func (p *observableCompleter) otelSetup() {
}

func (p *observableCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "chat "+p.model)
	defer span.End()

	timestamp := time.Now()

	result, err := p.completer.Complete(ctx, messages, options)

	if result != nil {
		duration := time.Since(timestamp).Seconds()

		providerName := genaiconv.ProviderNameAttr(p.provider)
		providerModel := p.model

		if result.Model != "" {
			providerModel = result.Model
		}

		p.operationDurationMetric.Record(ctx, duration,
			genaiconv.OperationNameChat,
			providerName,
			p.operationDurationMetric.AttrRequestModel(p.model),
			p.operationDurationMetric.AttrResponseModel(providerModel),
		)

		if result.Usage != nil {
			if result.Usage.InputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(result.Usage.InputTokens),
					genaiconv.OperationNameChat,
					providerName,
					genaiconv.TokenTypeInput,
					p.tokenUsageMetric.AttrRequestModel(p.model),
					p.tokenUsageMetric.AttrResponseModel(providerModel),
				)
			}

			if result.Usage.OutputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(result.Usage.OutputTokens),
					genaiconv.OperationNameChat,
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
