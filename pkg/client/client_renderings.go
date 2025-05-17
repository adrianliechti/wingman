package client

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type RenderingService struct {
	Options []RequestOption
}

func NewRenderingService(opts ...RequestOption) RenderingService {
	return RenderingService{
		Options: opts,
	}
}

type Rendering = provider.Rendering

type RenderOptions = provider.RenderOptions

type RenderingRequest struct {
	RenderOptions

	Model string

	Input string
}

func (r *RenderingService) New(ctx context.Context, input RenderingRequest, opts ...RequestOption) (*Rendering, error) {
	cfg := newRequestConfig(append(r.Options, opts...)...)
	url := strings.TrimRight(cfg.URL, "/") + "/v1/"

	options := []openai.Option{}

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if cfg.Client != nil {
		options = append(options, openai.WithClient(cfg.Client))
	}

	p, err := openai.NewRenderer(url, input.Model, options...)

	if err != nil {
		return nil, err
	}

	return p.Render(ctx, input.Input, &input.RenderOptions)
}
