package otel

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
)

type spanCompleter struct {
	name string

	completer provider.Completer
}

func NewCompleterSpan(name string, p provider.Completer) provider.Completer {
	return &spanCompleter{
		name:      name,
		completer: p,
	}
}

func (p *spanCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		ctx, span := otel.Tracer(instrumentationName).Start(ctx, p.name)
		defer span.End()

		for completion, err := range p.completer.Complete(ctx, messages, options) {
			if !yield(completion, err) {
				return
			}
		}
	}
}
