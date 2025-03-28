package memory

import (
	"github.com/adrianliechti/wingman/pkg/index"
)

type Option func(*Provider)

func WithEmbedder(embedder index.Embedder) Option {
	return func(p *Provider) {
		p.embedder = embedder
	}
}

func WithReranker(reranker index.Reranker) Option {
	return func(p *Provider) {
		p.reranker = reranker
	}
}
