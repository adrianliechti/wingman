package google

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/genai"
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

func (e *Embedder) Embed(ctx context.Context, texts []string, options *provider.EmbedOptions) (*provider.Embedding, error) {
	if options == nil {
		options = new(provider.EmbedOptions)
	}

	client, err := e.newClient(ctx)

	if err != nil {
		return nil, err
	}

	var contents []*genai.Content

	for _, text := range texts {
		contents = append(contents, genai.NewContentFromText(text, genai.RoleUser))
	}

	var config *genai.EmbedContentConfig

	if options.Dimensions != nil {
		dim := int32(*options.Dimensions)

		config = &genai.EmbedContentConfig{
			OutputDimensionality: &dim,
		}
	}

	resp, err := client.Models.EmbedContent(ctx, e.model, contents, config)

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
