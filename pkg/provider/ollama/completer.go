package ollama

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type Completer = openai.Completer

func NewCompleter(url, model string, options ...Option) (*Completer, error) {
	if url == "" {
		url = "http://localhost:11434"
	}

	url = strings.TrimRight(url, "/")
	url = strings.TrimSuffix(url, "/v1")

	cfg := &Config{}

	for _, option := range options {
		option(cfg)
	}

	return openai.NewCompleter(url+"/v1", model, cfg.options...)
}
