package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/llama/pkg/provider"
	"github.com/adrianliechti/llama/pkg/provider/llama"
	"github.com/adrianliechti/llama/pkg/provider/openai"
	"github.com/adrianliechti/llama/pkg/provider/sentencetransformers"
)

func (c *Config) registerProviders(f *configFile) error {
	for _, cfg := range f.Providers {
		p, err := createProvider(cfg)

		if err != nil {
			return err
		}

		for id, cfg := range cfg.Models {
			c.models[id] = Model{
				ID: id,

				model: cfg.ID,
			}

			c.providers[id] = p
		}
	}

	return nil
}

func createProvider(cfg providerConfig) (provider.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "openai":
		return openaiProvider(cfg)

	case "llama":
		return llamaProvider(cfg)

	case "sentence-transformers":
		return sentencetransformersProvider(cfg)

	default:
		return nil, errors.New("invalid provider type: " + cfg.Type)
	}
}

func openaiProvider(cfg providerConfig) (provider.Provider, error) {
	var options []openai.Option

	if cfg.URL != "" {
		options = append(options, openai.WithURL(cfg.URL))
	}

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	return openai.New(options...)
}

func llamaProvider(cfg providerConfig) (provider.Provider, error) {
	var options []llama.Option

	if len(cfg.Models) > 1 {
		return nil, errors.New("multiple models not supported for llama provider")
	}

	var prompt string
	var template string

	for _, v := range cfg.Models {
		prompt = v.Prompt
		template = v.Template

		break
	}

	if prompt != "" {
		options = append(options, llama.WithSystem(prompt))
	}

	switch strings.ToLower(template) {
	case "chatml":
		options = append(options, llama.WithPromptTemplate(&llama.PromptChatML{}))

	case "llama":
		options = append(options, llama.WithPromptTemplate(&llama.PromptLLAMA{}))

	case "mistral":
		options = append(options, llama.WithPromptTemplate(&llama.PromptMistral{}))

	default:
		return nil, errors.New("invalid prompt template: " + template)
	}

	return llama.New(cfg.URL, options...)
}

func sentencetransformersProvider(cfg providerConfig) (provider.Provider, error) {
	var options []sentencetransformers.Option

	if len(cfg.Models) > 1 {
		return nil, errors.New("multiple models not supported for sentence-transformers provider")
	}

	return sentencetransformers.New(cfg.URL, options...)
}
