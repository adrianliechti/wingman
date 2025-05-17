package provider

import (
	"context"
)

type Embedder interface {
	Embed(ctx context.Context, texts []string) (*Embedding, error)
}

type Embedding struct {
	Model string

	Embeddings [][]float32

	Usage *Usage
}
