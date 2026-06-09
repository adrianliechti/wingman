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

	tokenUsageMetric         genaiconv.ClientTokenUsage
	operationDurationMetric  genaiconv.ClientOperationDuration
	timeToFirstChunkMetric   genaiconv.ClientOperationTimeToFirstChunk
	timePerOutputChunkMetric genaiconv.ClientOperationTimePerOutputChunk
}

func NewCompleter(provider, model string, p provider.Completer) Completer {
	meter := otel.Meter(instrumentationName)

	tokenUsageMetric, _ := genaiconv.NewClientTokenUsage(meter)
	operationDurationMetric, _ := genaiconv.NewClientOperationDuration(meter)
	timeToFirstChunkMetric, _ := genaiconv.NewClientOperationTimeToFirstChunk(meter)
	timePerOutputChunkMetric, _ := genaiconv.NewClientOperationTimePerOutputChunk(meter)

	return &observableCompleter{
		completer: p,

		model:    model,
		provider: provider,

		tokenUsageMetric:         tokenUsageMetric,
		operationDurationMetric:  operationDurationMetric,
		timeToFirstChunkMetric:   timeToFirstChunkMetric,
		timePerOutputChunkMetric: timePerOutputChunkMetric,
	}
}

func (p *observableCompleter) otelSetup() {
}

func (p *observableCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		ctx, span := otel.Tracer(instrumentationName).Start(ctx, GenAISpanName(genaiconv.OperationNameChat, p.model), trace.WithSpanKind(trace.SpanKindClient))
		defer span.End()

		var tools []provider.Tool
		if options != nil {
			tools = options.Tools
		}

		if span.IsRecording() {
			span.SetAttributes(KeyValues(
				RequestAttrs(semconv.GenAIOperationNameChat, p.provider, p.model),
				[]KeyValue{semconv.GenAIRequestStream(true)},
				RequestOptionAttrs(options),
				SystemInstructionsAttrs(messages),
				PromptAttrs(messages),
				ToolDefinitionsAttrs(tools),
			)...)
		}

		timestamp := time.Now()
		providerName := genaiconv.ProviderNameAttr(p.provider)

		var firstChunkAt time.Time
		var prevChunkAt time.Time

		var lastResult *provider.Completion
		var lastErr error

		// Defer metric recording so consumer cancellation (yield returning
		// false) still records duration + token usage instead of silently
		// dropping the observation.
		defer func() {
			duration := time.Since(timestamp).Seconds()
			providerModel := p.model

			if lastResult != nil {
				if lastResult.Model != "" {
					providerModel = lastResult.Model
				}

				if span.IsRecording() {
					responseAttrs := []KeyValue{
						semconv.GenAIResponseModel(providerModel),
						semconv.GenAIResponseFinishReasons(finishReason(lastResult)),
					}
					if lastResult.ID != "" {
						responseAttrs = append(responseAttrs, semconv.GenAIResponseID(lastResult.ID))
					}

					span.SetAttributes(KeyValues(
						responseAttrs,
						UsageAttrs(lastResult.Usage),
						CompletionAttrs(lastResult),
					)...)
				}
			}

			attrs := MetricAttrs(ctx, p.model, providerModel)

			if lastErr != nil {
				attrs = append(attrs, p.operationDurationMetric.AttrErrorType(ErrorTypeAttr(lastErr)))
			}

			p.operationDurationMetric.Record(ctx, duration,
				genaiconv.OperationNameChat, providerName, attrs...)

			if !firstChunkAt.IsZero() {
				ttfc := firstChunkAt.Sub(timestamp).Seconds()
				if span.IsRecording() {
					span.SetAttributes(semconv.GenAIResponseTimeToFirstChunk(ttfc))
				}
				p.timeToFirstChunkMetric.Record(ctx, ttfc,
					genaiconv.OperationNameChat, providerName, attrs...)
			}

			if lastResult == nil || lastResult.Usage == nil {
				return
			}

			tokenAttrs := attrs

			if lastResult.Usage.InputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.InputTokens),
					genaiconv.OperationNameChat, providerName, genaiconv.TokenTypeInput, tokenAttrs...)
			}
			if lastResult.Usage.OutputTokens > 0 {
				p.tokenUsageMetric.Record(ctx, int64(lastResult.Usage.OutputTokens),
					genaiconv.OperationNameChat, providerName, genaiconv.TokenTypeOutput, tokenAttrs...)
			}
		}()

		attrs := MetricAttrs(ctx, p.model, p.model)

		for completion, err := range p.completer.Complete(ctx, messages, options) {
			if err != nil {
				lastErr = err
				RecordError(span, err)
				yield(nil, err)
				return
			}

			now := time.Now()
			if firstChunkAt.IsZero() {
				firstChunkAt = now
			} else {
				p.timePerOutputChunkMetric.Record(ctx, now.Sub(prevChunkAt).Seconds(),
					genaiconv.OperationNameChat, providerName, attrs...)
			}
			prevChunkAt = now

			lastResult = completion

			if !yield(completion, nil) {
				return
			}
		}
	}
}
