package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/segmenter"

	"go.opentelemetry.io/otel"
)

type Segmenter interface {
	Observable
	segmenter.Provider
}

type observableSegmenter struct {
	model    string
	provider string

	segmenter segmenter.Provider
}

func NewSegmenter(provider string, p segmenter.Provider) Segmenter {
	return &observableSegmenter{
		segmenter: p,

		model:    "default",
		provider: provider,
	}
}

func (p *observableSegmenter) otelSetup() {
}

func (p *observableSegmenter) Segment(ctx context.Context, input string, options *segmenter.SegmentOptions) ([]segmenter.Segment, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "segment "+p.model)
	defer span.End()

	result, err := p.segmenter.Segment(ctx, input, options)

	return result, err
}
