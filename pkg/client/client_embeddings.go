package client

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type EmbeddingService struct {
	Options []RequestOption
}

func NewEmbeddingService(opts ...RequestOption) EmbeddingService {
	return EmbeddingService{
		Options: opts,
	}
}

type Embedding = provider.Embedding

type EmbeddingsRequest struct {
	Model string

	Texts []string
}

func (r *EmbeddingService) New(ctx context.Context, input EmbeddingsRequest, opts ...RequestOption) (*Embedding, error) {
	cfg := newRequestConfig(append(r.Options, opts...)...)
	url := strings.TrimRight(cfg.URL, "/") + "/v1/"

	options := []openai.Option{}

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if cfg.Client != nil {
		options = append(options, openai.WithClient(cfg.Client))
	}

	p, err := openai.NewEmbedder(url, input.Model, options...)

	if err != nil {
		return nil, err
	}

	return p.Embed(ctx, input.Texts)
}
