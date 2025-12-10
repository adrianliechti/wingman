package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
)

type Renderer interface {
	Observable
	provider.Renderer
}

type observableRenderer struct {
	model    string
	provider string

	renderer provider.Renderer
}

func NewRenderer(provider, model string, p provider.Renderer) Renderer {
	return &observableRenderer{
		renderer: p,

		model:    model,
		provider: provider,
	}
}

func (p *observableRenderer) otelSetup() {
}

func (p *observableRenderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "render "+p.model)
	defer span.End()

	result, err := p.renderer.Render(ctx, input, options)

	return result, err
}
