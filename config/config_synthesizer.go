package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func (cfg *Config) RegisterSynthesizer(id string, p provider.Synthesizer) {
	cfg.RegisterModel(id)

	if cfg.synthesizer == nil {
		cfg.synthesizer = make(map[string]provider.Synthesizer)
	}

	if _, ok := cfg.synthesizer[""]; !ok {
		cfg.synthesizer[""] = p
	}

	cfg.synthesizer[id] = p
}

func (cfg *Config) Synthesizer(id string) (provider.Synthesizer, error) {
	if cfg.synthesizer != nil {
		if s, ok := cfg.synthesizer[id]; ok {
			return s, nil
		}
	}

	return nil, errors.New("synthesizer not found: " + id)
}

func createSynthesizer(cfg providerConfig, model modelContext) (provider.Synthesizer, error) {
	switch strings.ToLower(cfg.Type) {
	case "openai", "openai-compatible":
		return openaiSynthesizer(cfg, model)

	default:
		return nil, errors.New("invalid synthesizer type: " + cfg.Type)
	}
}

func openaiSynthesizer(cfg providerConfig, model modelContext) (provider.Synthesizer, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	return openai.NewSynthesizer(cfg.URL, model.ID, options...)
}
