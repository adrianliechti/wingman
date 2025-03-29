package otel

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/chain"
	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Chain interface {
	Observable
	chain.Provider
}

type observableChain struct {
	name    string
	library string

	model    string
	provider string

	chain chain.Provider
}

func NewChain(provider, model string, p chain.Provider) Chain {
	library := strings.ToLower(provider)

	return &observableChain{
		chain: p,

		name:    strings.TrimSuffix(strings.ToLower(provider), "-chain") + "-chain",
		library: library,

		model:    model,
		provider: provider,
	}
}

func (p *observableChain) otelSetup() {
}

func (p *observableChain) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	ctx, span := otel.Tracer(p.library).Start(ctx, p.name)
	defer span.End()

	result, err := p.chain.Complete(ctx, messages, options)

	meterRequest(ctx, p.library, p.provider, "complete", p.model)

	if len(messages) > 0 {
		input := messages[len(messages)-1].Content

		if input != nil {
			span.SetAttributes(attribute.String("input", input.Text()))
		}
	}

	if result != nil {
		if result.Message.Content != nil {
			span.SetAttributes(attribute.String("output", result.Message.Content.Text()))
		}
	}

	return result, err
}
