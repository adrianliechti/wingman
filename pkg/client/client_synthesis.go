package client

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type SynthesisService struct {
	Options []RequestOption
}

func NewSynthesisService(opts ...RequestOption) SynthesisService {
	return SynthesisService{
		Options: opts,
	}
}

type Synthesis = provider.Synthesis
type SynthesizeOptions = provider.SynthesizeOptions

type SynthesizeRequest struct {
	SynthesizeOptions

	Model string

	Input string
}

func (r *SynthesisService) New(ctx context.Context, input SynthesizeRequest, opts ...RequestOption) (*Synthesis, error) {
	cfg := newRequestConfig(append(r.Options, opts...)...)
	url := strings.TrimRight(cfg.URL, "/") + "/v1/"

	options := []openai.Option{}

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if cfg.Client != nil {
		options = append(options, openai.WithClient(cfg.Client))
	}

	p, err := openai.NewSynthesizer(url, input.Model, options...)

	if err != nil {
		return nil, err
	}

	return p.Synthesize(ctx, input.Input, &input.SynthesizeOptions)
}
