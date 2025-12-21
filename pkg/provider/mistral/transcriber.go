package mistral

import (
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type Transcriber = openai.Transcriber

func NewTranscriber(model string, options ...Option) (*Transcriber, error) {
	url := "https://api.mistral.ai/v1/"

	cfg := &Config{}

	for _, option := range options {
		option(cfg)
	}

	return openai.NewTranscriber(url, model, cfg.options...)
}
