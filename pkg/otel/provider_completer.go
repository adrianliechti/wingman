package otel

import (
	"context"
	"iter"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/semconv/v1.40.0/genaiconv"
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

		if span.IsRecording() {
			span.SetAttributes(KeyValues(
				RequestAttrs(semconv.GenAIOperationNameChat, p.provider, p.model, ""),
				EndUserAttrs(ctx),
				PromptAttrs(messages),
			)...)
		}

		timestamp := time.Now()

		var lastResult *provider.Completion

		for completion, err := range p.completer.Complete(ctx, messages, options) {
			if err != nil {
				span.RecordError(err)
				span.SetAttributes(semconv.ErrorType(err))
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

			if span.IsRecording() {
				span.SetAttributes(KeyValues(
					[]KeyValue{semconv.GenAIResponseModel(providerModel)},
					UsageAttrs(lastResult.Usage),
					CompletionAttrs(lastResult),
				)...)
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

				if lastResult.Usage.CacheCreationInputTokens > 0 {
					p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.CacheCreationInputTokens),
						genaiconv.OperationNameChat,
						providerName,
						TokenTypeCacheCreation,
						KeyValues([]KeyValue{
							p.tokenUsageMetric.AttrRequestModel(p.model),
							p.tokenUsageMetric.AttrResponseModel(providerModel),
						}, EndUserAttrs(ctx))...,
					)
				}

				if lastResult.Usage.CacheReadInputTokens > 0 {
					p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.CacheReadInputTokens),
						genaiconv.OperationNameChat,
						providerName,
						TokenTypeCacheRead,
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
