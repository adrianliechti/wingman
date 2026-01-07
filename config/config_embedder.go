package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/custom"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/huggingface"
	"github.com/adrianliechti/wingman/pkg/provider/jina"
	"github.com/adrianliechti/wingman/pkg/provider/llama"
	"github.com/adrianliechti/wingman/pkg/provider/mistral"
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
	case "gemini", "google":
		return googleEmbedder(cfg, model)

	case "huggingface":
		return huggingfaceEmbedder(cfg, model)

	case "jina":
		return jinaEmbedder(cfg, model)

	case "llama":
		return llamaEmbedder(cfg, model)

	case "mistral":
		return mistralEmbedder(cfg, model)

	case "ollama":
		return ollamaEmbedder(cfg, model)

	case "openai", "openai-compatible":
		return openaiEmbedder(cfg, model)

	case "custom":
		return customEmbedder(cfg, model)

	default:
		return nil, errors.New("invalid embedder type: " + cfg.Type)
	}
}

func googleEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []google.Option

	if cfg.Token != "" {
		options = append(options, google.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, google.WithClient(model.Client))
	}

	return google.NewEmbedder(model.ID, options...)
}

func huggingfaceEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []huggingface.Option

	if cfg.Token != "" {
		options = append(options, huggingface.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, huggingface.WithClient(model.Client))
	}

	return huggingface.NewEmbedder(cfg.URL, model.ID, options...)
}

func jinaEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, jina.WithClient(model.Client))
	}

	return jina.NewEmbedder(cfg.URL, model.ID, options...)
}

func llamaEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []llama.Option

	if model.Client != nil {
		options = append(options, llama.WithClient(model.Client))
	}

	return llama.NewEmbedder(model.ID, cfg.URL, options...)
}

func mistralEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []mistral.Option

	if cfg.Token != "" {
		options = append(options, mistral.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, mistral.WithClient(model.Client))
	}

	return mistral.NewEmbedder(model.ID, options...)
}

func ollamaEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []ollama.Option

	if model.Client != nil {
		options = append(options, ollama.WithClient(model.Client))
	}

	return ollama.NewEmbedder(cfg.URL, model.ID, options...)
}

func openaiEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, openai.WithClient(model.Client))
	}

	return openai.NewEmbedder(cfg.URL, model.ID, options...)
}

func customEmbedder(cfg providerConfig, model modelContext) (provider.Embedder, error) {
	var options []custom.Option

	return custom.NewEmbedder(cfg.URL, options...)
}
