package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/pkg/provider/custom"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/huggingface"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func (cfg *Config) RegisterCompleter(id string, p provider.Completer) {
	cfg.RegisterModel(id)

	if cfg.completer == nil {
		cfg.completer = make(map[string]provider.Completer)
	}

	if _, ok := cfg.completer[""]; !ok {
		cfg.completer[""] = p
	}

	cfg.completer[id] = p
}

func (cfg *Config) Completer(id string) (provider.Completer, error) {
	if cfg.completer != nil {
		if c, ok := cfg.completer[id]; ok {
			return c, nil
		}
	}

	if cfg.chains != nil {
		if c, ok := cfg.chains[id]; ok {
			return c, nil
		}
	}

	return nil, errors.New("completer not found: " + id)
}

func createCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	switch strings.ToLower(cfg.Type) {
	case "anthropic":
		return anthropicCompleter(cfg, model)

	case "bedrock":
		return bedrockCompleter(cfg, model)

	case "gemini", "google":
		return googleCompleter(cfg, model)

	case "huggingface":
		return huggingfaceCompleter(cfg, model)

	case "llama":
		cfg.URL = normalizeURL(cfg.URL, "/v1")
		return openaiCompleter(cfg, model, true)

	case "mistral":
		if cfg.URL == "" {
			cfg.URL = "https://api.mistral.ai/v1/"
		}

		return openaiCompleter(cfg, model, true)

	case "ollama":
		if cfg.URL == "" {
			cfg.URL = "http://localhost:11434"
		}

		cfg.URL = normalizeURL(cfg.URL, "/v1")
		return openaiCompleter(cfg, model, true)

	case "nim", "nvidia":
		return openaiCompleter(cfg, model, true)

	case "openai":
		return openaiCompleter(cfg, model, false)

	case "openai-compatible":
		return openaiCompleter(cfg, model, true)

	case "custom":
		return customCompleter(cfg, model)

	default:
		return nil, errors.New("invalid completer type: " + cfg.Type)
	}
}

func anthropicCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []anthropic.Option

	if cfg.Token != "" {
		options = append(options, anthropic.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, anthropic.WithClient(model.Client))
	}

	return anthropic.NewCompleter(cfg.URL, model.ID, options...)
}

func bedrockCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []bedrock.Option

	if model.Client != nil {
		options = append(options, bedrock.WithClient(model.Client))
	}

	return bedrock.NewCompleter(model.ID, options...)
}

func googleCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []google.Option

	if cfg.Token != "" {
		options = append(options, google.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, google.WithClient(model.Client))
	}

	return google.NewCompleter(model.ID, options...)
}

func huggingfaceCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []huggingface.Option

	if cfg.Token != "" {
		options = append(options, huggingface.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, huggingface.WithClient(model.Client))
	}

	return huggingface.NewCompleter(cfg.URL, model.ID, options...)
}

func openaiCompleter(cfg providerConfig, model modelContext, useLegacy bool) (provider.Completer, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, openai.WithClient(model.Client))
	}

	if useLegacy {
		return openai.NewCompleter(cfg.URL, model.ID, options...)
	}

	return openai.NewResponder(cfg.URL, model.ID, options...)
}

func customCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []custom.Option

	return custom.NewCompleter(cfg.URL, options...)
}
