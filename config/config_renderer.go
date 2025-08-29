package config

import (
	"errors"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/provider/replicate"
	"github.com/adrianliechti/wingman/pkg/provider/replicate/flux"
)

func (cfg *Config) RegisterRenderer(id string, p provider.Renderer) {
	cfg.RegisterModel(id)

	if cfg.renderer == nil {
		cfg.renderer = make(map[string]provider.Renderer)
	}

	if _, ok := cfg.renderer[""]; !ok {
		cfg.renderer[""] = p
	}

	cfg.renderer[id] = p
}

func (cfg *Config) Renderer(id string) (provider.Renderer, error) {
	if cfg.renderer != nil {
		if t, ok := cfg.renderer[id]; ok {
			return t, nil
		}
	}

	return nil, errors.New("renderer not found: " + id)
}

func createRenderer(cfg providerConfig, model modelContext) (provider.Renderer, error) {
	switch strings.ToLower(cfg.Type) {
	case "gemini", "google":
		return googleRenderer(cfg, model)

	case "openai":
		return openaiRenderer(cfg, model)

	case "replicate":
		return replicateRenderer(cfg, model)

	default:
		return nil, errors.New("invalid renderer type: " + cfg.Type)
	}
}

func googleRenderer(cfg providerConfig, model modelContext) (provider.Renderer, error) {
	var options []google.Option

	if cfg.Token != "" {
		options = append(options, google.WithToken(cfg.Token))
	}

	return google.NewRenderer(model.ID, options...)
}

func openaiRenderer(cfg providerConfig, model modelContext) (provider.Renderer, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	return openai.NewRenderer(cfg.URL, model.ID, options...)
}

func replicateRenderer(cfg providerConfig, model modelContext) (provider.Renderer, error) {
	if slices.Contains(flux.SupportedModels, model.ID) {
		var options []replicate.Option

		if cfg.Token != "" {
			options = append(options, replicate.WithToken(cfg.Token))
		}

		return flux.NewRenderer(model.ID, options...)
	}

	return nil, errors.New("model not supported: " + model.ID)
}
