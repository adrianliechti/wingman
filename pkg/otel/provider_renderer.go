package otel

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Renderer interface {
	Observable
	provider.Renderer
}

type observableRenderer struct {
	name    string
	library string

	model    string
	provider string

	renderer provider.Renderer
}

func NewRenderer(provider, model string, p provider.Renderer) Renderer {
	library := strings.ToLower(provider)

	return &observableRenderer{
		renderer: p,

		name:    strings.TrimSuffix(strings.ToLower(provider), "-renderer") + "-renderer",
		library: library,

		model:    model,
		provider: provider,
	}
}

func (p *observableRenderer) otelSetup() {
}

func (p *observableRenderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	ctx, span := otel.Tracer(p.library).Start(ctx, p.name)
	defer span.End()

	result, err := p.renderer.Render(ctx, input, options)

	meterRequest(ctx, p.library, p.provider, "render", p.model)

	if EnableDebug {
		span.SetAttributes(attribute.String("input", input))
	}

	return result, err
}
