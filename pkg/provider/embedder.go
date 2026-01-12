package provider

import (
	"context"
)

type Embedder interface {
	Embed(ctx context.Context, texts []string, options *EmbedOptions) (*Embedding, error)
}

type EmbedOptions struct {
	Dimensions *int
}

type Embedding struct {
	Model string

	Embeddings [][]float32

	Usage *Usage
}
