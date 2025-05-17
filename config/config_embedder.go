package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/azure"
	"github.com/adrianliechti/wingman/pkg/provider/cohere"
	"github.com/adrianliechti/wingman/pkg/provider/custom"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/huggingface"
	"github.com/adrianliechti/wingman/pkg/provider/jina"
	"github.com/adrianliechti/wingman/pkg/provider/llama"
	"github.com/adrianliechti/wingman/pkg/provider/ollama"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func (cfg *Config) RegisterEmbedder(id string, p provider.Embedder) {
	cfg.RegisterModel(id)

	if cfg.embedder == nil {
		cfg.embedder = make(map[string]provider.Embedder)
	}

	if _, ok := cfg.embedder[""]; !ok {
		cfg.embedder[""] = p
	}

	cfg.embedder[id] = p
}

func (cfg *Config) Embedder(id string) (provider.Embedder, error) {
	if cfg.embedder != nil {
		if e, ok := cfg.embedder[id]; ok {
			return e, nil
		}
	}

	return nil, errors.New("embedder not found: " + id)
}

func createEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	switch strings.ToLower(cfg.Type) {
	case "azure":
		return azureEmbedder(cfg, model)

	case "cohere":
		return cohereEmbedder(cfg, model)

	case "github":
		return azureEmbedder(cfg, model)

	case "google":
		return googleEmbedder(cfg, model)

	case "huggingface":
		return huggingfaceEmbedder(cfg, model)

	case "jina":
		return jinaEmbedder(cfg, model)

	case "llama":
		return llamaEmbedder(cfg, model)

	case "ollama":
		return ollamaEmbedder(cfg, model)

	case "openai":
		return openaiEmbedder(cfg, model)

	case "custom":
		return customEmbedder(cfg, model)

	default:
		return nil, errors.New("invalid embedder type: " + cfg.Type)
	}
}

func azureEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []azure.Option

	if cfg.Token != "" {
		options = append(options, azure.WithToken(cfg.Token))
	}

	return azure.NewEmbedder(cfg.URL, model.ID, options...)
}

func cohereEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []cohere.Option

	if cfg.Token != "" {
		options = append(options, cohere.WithToken(cfg.Token))
	}

	return cohere.NewEmbedder(model.ID, options...)
}

func googleEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []google.Option

	if cfg.Token != "" {
		options = append(options, google.WithToken(cfg.Token))
	}

	return google.NewEmbedder(model.ID, options...)
}

func huggingfaceEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []huggingface.Option

	if cfg.Token != "" {
		options = append(options, huggingface.WithToken(cfg.Token))
	}

	return huggingface.NewEmbedder(cfg.URL, model.ID, options...)
}

func jinaEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	return jina.NewEmbedder(cfg.URL, model.ID, options...)
}

func llamaEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []llama.Option

	return llama.NewEmbedder(model.ID, cfg.URL, options...)
}

func ollamaEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []ollama.Option

	return ollama.NewEmbedder(cfg.URL, model.ID, options...)
}

func openaiEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	return openai.NewEmbedder(cfg.URL, model.ID, options...)
}

func customEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []custom.Option

	return custom.NewEmbedder(cfg.URL, options...)
}
