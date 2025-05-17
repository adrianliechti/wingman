package google

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/generative-ai-go/genai"
)

var _ provider.Embedder = (*Embedder)(nil)

type Embedder struct {
	*Config
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
	}, nil
}

func (e *Embedder) Embed(ctx context.Context, texts []string) (*provider.Embedding, error) {
	client, err := genai.NewClient(ctx, e.Options()...)

	if err != nil {
		return nil, err
	}

	defer client.Close()

	model := client.EmbeddingModel(e.model)

	batch := model.NewBatch()

	for _, text := range texts {
		batch.AddContent(genai.Text(text))
	}

	resp, err := model.BatchEmbedContents(ctx, batch)

	if err != nil {
		return nil, convertError(err)
	}

	result := &provider.Embedding{
		Model: e.model,
	}

	for _, e := range resp.Embeddings {
		result.Embeddings = append(result.Embeddings, e.Values)
	}

	return result, nil
}
