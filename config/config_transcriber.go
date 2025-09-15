package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/groq"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/provider/whisper"
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
	case "groq":
		return groqTranscriber(cfg, model)

	case "openai", "openai-compatible":
		return openaiTranscriber(cfg, model)

	case "whisper":
		return whisperTranscriber(cfg, model)

	default:
		return nil, errors.New("invalid transcriber type: " + cfg.Type)
	}
}

func groqTranscriber(cfg providerConfig, model modelContext) (provider.Transcriber, error) {
	var options []groq.Option

	if cfg.Token != "" {
		options = append(options, groq.WithToken(cfg.Token))
	}

	return groq.NewTranscriber(cfg.URL, model.ID, options...)
}

func openaiTranscriber(cfg providerConfig, model modelContext) (provider.Transcriber, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	return openai.NewTranscriber(cfg.URL, model.ID, options...)
}

func whisperTranscriber(cfg providerConfig, model modelContext) (provider.Transcriber, error) {
	var options []whisper.Option

	return whisper.NewTranscriber(cfg.URL, model.ID, options...)
}
