package otel

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Completer interface {
	Observable
	provider.Completer
}

type observableCompleter struct {
	name    string
	library string

	model    string
	provider string

	completer provider.Completer
}

func NewCompleter(provider, model string, p provider.Completer) Completer {
	library := strings.ToLower(provider)

	return &observableCompleter{
		completer: p,

		name:    strings.TrimSuffix(strings.ToLower(provider), "-completer") + "-completer",
		library: library,

		model:    model,
		provider: provider,
	}
}

func (p *observableCompleter) otelSetup() {
}

func (p *observableCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	ctx, span := otel.Tracer(p.library).Start(ctx, p.name)
	defer span.End()

	result, err := p.completer.Complete(ctx, messages, options)

	meterRequest(ctx, p.library, p.provider, "complete", p.model)

	if EnableDebug {
		if len(messages) > 0 {
			input := messages[len(messages)-1].Text()

			if input != "" {
				span.SetAttributes(attribute.String("input", input))
			}
		}

		if result != nil {
			output := result.Message.Text()

			if output != "" {
				span.SetAttributes(attribute.String("output", output))
			}
		}
	}

	if result != nil {
		if result.Usage != nil {
			tokens := int64(result.Usage.InputTokens) + int64(result.Usage.OutputTokens)
			meterTokens(ctx, p.library, p.provider, "complete", p.model, tokens)
		}
	}

	return result, err
}
