package otel

import (
	"context"
	"iter"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
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
		ctx, span := otel.Tracer(instrumentationName).Start(ctx, GenAISpanName(genaiconv.OperationNameChat, p.model), trace.WithSpanKind(trace.SpanKindClient))
		defer span.End()

		if span.IsRecording() {
			span.SetAttributes(KeyValues(
				RequestAttrs(semconv.GenAIOperationNameChat, p.provider, p.model),
				EndUserAttrs(ctx),
				PromptAttrs(messages),
			)...)
		}

		timestamp := time.Now()

		var lastResult *provider.Completion
		var lastErr error

		// Defer metric recording so consumer cancellation (yield returning
		// false) still records duration + token usage instead of silently
		// dropping the observation.
		defer func() {
			duration := time.Since(timestamp).Seconds()
			providerName := genaiconv.ProviderNameAttr(p.provider)
			providerModel := p.model

			if lastResult != nil {
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
			}

			modelAttrs := []KeyValue{
				p.operationDurationMetric.AttrRequestModel(p.model),
				p.operationDurationMetric.AttrResponseModel(providerModel),
			}
			userAttrs := EndUserAttrs(ctx)

			durationAttrs := KeyValues(modelAttrs, userAttrs)
			if lastErr != nil {
				durationAttrs = append(durationAttrs, p.operationDurationMetric.AttrErrorType(ErrorTypeAttr(lastErr)))
			}

			p.operationDurationMetric.Record(ctx, duration,
				genaiconv.OperationNameChat, providerName, durationAttrs...)

			if lastResult == nil || lastResult.Usage == nil {
				return
			}

			tokenAttrs := KeyValues(modelAttrs, userAttrs)

			if lastResult.Usage.InputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.InputTokens),
					genaiconv.OperationNameChat, providerName, genaiconv.TokenTypeInput, tokenAttrs...)
			}
			if lastResult.Usage.OutputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.OutputTokens),
					genaiconv.OperationNameChat, providerName, genaiconv.TokenTypeOutput, tokenAttrs...)
			}
		}()

		for completion, err := range p.completer.Complete(ctx, messages, options) {
			if err != nil {
				lastErr = err
				RecordError(span, err)
				yield(nil, err)
				return
			}

			lastResult = completion

			if !yield(completion, nil) {
				return
			}
		}
	}
}
