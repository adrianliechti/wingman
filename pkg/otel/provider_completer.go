package otel

import (
	"context"
	"iter"
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

func (p *observableCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		ctx, span := otel.Tracer(instrumentationName).Start(ctx, "chat "+p.model)
		defer span.End()

		timestamp := time.Now()

		var lastResult *provider.Completion

		for completion, err := range p.completer.Complete(ctx, messages, options) {
			if err != nil {
				yield(nil, err)
				return
			}

			lastResult = completion

			if !yield(completion, nil) {
				return
			}
		}

		if lastResult != nil {
			duration := time.Since(timestamp).Seconds()

			providerName := genaiconv.ProviderNameAttr(p.provider)
			providerModel := p.model

			if lastResult.Model != "" {
				providerModel = lastResult.Model
			}

			p.operationDurationMetric.Record(ctx, duration,
				genaiconv.OperationNameChat,
				providerName,
				KeyValues([]KeyValue{
					p.operationDurationMetric.AttrRequestModel(p.model),
					p.operationDurationMetric.AttrResponseModel(providerModel),
				}, EndUserAttrs(ctx))...,
			)

			if lastResult.Usage != nil {
				if lastResult.Usage.InputTokens > 0 {
					p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.InputTokens),
						genaiconv.OperationNameChat,
						providerName,
						genaiconv.TokenTypeInput,
						KeyValues([]KeyValue{
							p.tokenUsageMetric.AttrRequestModel(p.model),
							p.tokenUsageMetric.AttrResponseModel(providerModel),
						}, EndUserAttrs(ctx))...,
					)
				}

				if lastResult.Usage.OutputTokens > 0 {
					p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.OutputTokens),
						genaiconv.OperationNameChat,
						providerName,
						genaiconv.TokenTypeOutput,
						KeyValues([]KeyValue{
							p.tokenUsageMetric.AttrRequestModel(p.model),
							p.tokenUsageMetric.AttrResponseModel(providerModel),
						}, EndUserAttrs(ctx))...,
					)
				}
			}
		}
	}
}
