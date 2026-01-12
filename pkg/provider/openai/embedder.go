package openai

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
)

var _ provider.Embedder = (*Embedder)(nil)

type Embedder struct {
	*Config
	embeddings openai.EmbeddingService
}

func NewEmbedder(url, model string, options ...Option) (*Embedder, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Embedder{
		Config:     cfg,
		embeddings: openai.NewEmbeddingService(cfg.Options()...),
	}, nil
}

func (e *Embedder) Embed(ctx context.Context, texts []string, options *provider.EmbedOptions) (*provider.Embedding, error) {
	if options == nil {
		options = new(provider.EmbedOptions)
	}

	params := openai.EmbeddingNewParams{
		Model: e.model,

		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: texts,
		},

		EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
	}

	if options.Dimensions != nil {
		params.Dimensions = openai.Int(int64(*options.Dimensions))
	}

	embedding, err := e.embeddings.New(ctx, params)

	if err != nil {
		return nil, convertError(err)
	}

	result := &provider.Embedding{
		Model: e.model,
	}

	if embedding.Usage.PromptTokens > 0 {
		result.Usage = &provider.Usage{
			InputTokens:  int(embedding.Usage.PromptTokens),
			OutputTokens: 0,
		}
	}

	for _, e := range embedding.Data {
		result.Embeddings = append(result.Embeddings, toFloat32(e.Embedding))
	}

	return result, nil
}

func toFloat32(input []float64) []float32 {
	result := make([]float32, len(input))

	for i, v := range input {
		result[i] = float32(v)
	}

	return result
}
