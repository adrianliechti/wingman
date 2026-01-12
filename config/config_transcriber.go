package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/mistral"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func (cfg *Config) RegisterTranscriber(id string, p provider.Transcriber) {
	cfg.RegisterModel(id)

	if cfg.transcriber == nil {
		cfg.transcriber = make(map[string]provider.Transcriber)
	}

	if _, ok := cfg.transcriber[""]; !ok {
		cfg.transcriber[""] = p
	}

	cfg.transcriber[id] = p
}

func (cfg *Config) Transcriber(id string) (provider.Transcriber, error) {
	if cfg.transcriber != nil {
		if t, ok := cfg.transcriber[id]; ok {
			return t, nil
		}
	}

	return nil, errors.New("transcriber not found: " + id)
}

func createTranscriber(cfg providerConfig, model modelContext) (provider.Transcriber, error) {
	switch strings.ToLower(cfg.Type) {
	case "mistral":
		return mistralTranscriber(cfg, model)

	case "openai", "openai-compatible":
		return openaiTranscriber(cfg, model)

	default:
		return nil, errors.New("invalid transcriber type: " + cfg.Type)
	}
}

func mistralTranscriber(cfg providerConfig, model modelContext) (provider.Transcriber, error) {
	var options []mistral.Option

	if cfg.Token != "" {
		options = append(options, mistral.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, mistral.WithClient(model.Client))
	}

	return mistral.NewTranscriber(model.ID, options...)
}

func openaiTranscriber(cfg providerConfig, model modelContext) (provider.Transcriber, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if model.Client != nil {
		options = append(options, openai.WithClient(model.Client))
	}

	return openai.NewTranscriber(cfg.URL, model.ID, options...)
}
