package mistral

import (
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type Embedder = openai.Embedder

func NewEmbedder(model string, options ...Option) (*Embedder, error) {
	url := "https://api.mistral.ai/v1/"

	cfg := &Config{}

	for _, option := range options {
		option(cfg)
	}

	return openai.NewEmbedder(url, model, cfg.options...)
}
