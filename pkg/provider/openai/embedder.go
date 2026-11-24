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

	if len(embedding.Data) != len(texts) {
		return nil, &provider.ProviderError{Kind: provider.ErrorKindInternal, Type: "server_error", Message: "upstream embedding count does not match input count"}
	}
	result.Embeddings = make([][]float32, len(texts))
	seen := make([]bool, len(texts))
	for _, e := range embedding.Data {
		if e.Index < 0 || e.Index >= int64(len(texts)) || seen[e.Index] {
			return nil, &provider.ProviderError{Kind: provider.ErrorKindInternal, Type: "server_error", Message: "upstream returned invalid or duplicate embedding indices"}
		}
		seen[e.Index] = true
		result.Embeddings[e.Index] = toFloat32(e.Embedding)
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
