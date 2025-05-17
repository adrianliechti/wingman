package cohere

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	v2 "github.com/cohere-ai/cohere-go/v2"
	client "github.com/cohere-ai/cohere-go/v2/v2"
)

var _ provider.Embedder = (*Embedder)(nil)

type Embedder struct {
	*Config
	client *client.Client
}

func NewEmbedder(model string, options ...Option) (*Embedder, error) {
	cfg := &Config{
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Embedder{
		Config: cfg,
		client: client.NewClient(cfg.Options()...),
	}, nil
}

func (e *Embedder) Embed(ctx context.Context, texts []string) (*provider.Embedding, error) {
	req := &v2.V2EmbedRequest{
		Model: e.model,

		Texts: texts,

		InputType: v2.EmbedInputTypeSearchDocument,

		EmbeddingTypes: []v2.EmbeddingType{
			v2.EmbeddingTypeFloat,
		},
	}

	resp, err := e.client.Embed(ctx, req)

	if err != nil {
		return nil, convertError(err)
	}

	result := &provider.Embedding{
		Model: e.model,
	}

	for _, e := range resp.Embeddings.Float {
		result.Embeddings = append(result.Embeddings, toFloat32(e))
	}

	return result, nil
}
