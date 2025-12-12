package otel

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/semconv/v1.38.0/genaiconv"
)

type Renderer interface {
	Observable
	provider.Renderer
}

type observableRenderer struct {
	model    string
	provider string

	renderer provider.Renderer

	operationDurationMetric genaiconv.ClientOperationDuration
}

func NewRenderer(provider, model string, p provider.Renderer) Renderer {
	meter := otel.Meter(instrumentationName)

	operationDurationMetric, _ := genaiconv.NewClientOperationDuration(meter)

	return &observableRenderer{
		renderer: p,

		model:    model,
		provider: provider,

		operationDurationMetric: operationDurationMetric,
	}
}

func (p *observableRenderer) otelSetup() {
}

func (p *observableRenderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "render "+p.model)
	defer span.End()

	timestamp := time.Now()

	result, err := p.renderer.Render(ctx, input, options)

	if result != nil {
		duration := time.Since(timestamp).Seconds()

		providerName := genaiconv.ProviderNameAttr(p.provider)
		providerModel := p.model

		if result.Model != "" {
			providerModel = result.Model
		}

		p.operationDurationMetric.Record(ctx, duration,
			genaiconv.OperationNameGenerateContent,
			providerName,
			p.operationDurationMetric.AttrRequestModel(p.model),
			p.operationDurationMetric.AttrResponseModel(providerModel),
		)
	}

	return result, err
}
