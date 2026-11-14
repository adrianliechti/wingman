package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/provider/typesafe"
)

type registeredDecider struct {
	provider.Decider
	id string
}

func (cfg *Config) RegisterDecider(id string, p provider.Decider) {
	cfg.RegisterModel(id)
	if cfg.decider == nil {
		cfg.decider = make(map[string]registeredDecider)
	}
	entry := registeredDecider{Decider: p, id: id}
	if _, ok := cfg.decider[""]; !ok {
		cfg.decider[""] = entry
	}
	cfg.decider[id] = entry
}

// Decider returns the configured model alias as well as its provider so
// callers can apply access policies to the actual selection.
func (cfg *Config) Decider(id string) (string, provider.Decider, error) {
	if p, ok := cfg.decider[id]; ok {
		return p.id, p.Decider, nil
	}
	if id == "" {
		return "", nil, errors.New("no decision model configured")
	}
	return "", nil, errors.New("decision model not found: " + id)
}

func createDecider(cfg providerConfig, model modelContext) (provider.Decider, error) {
	if strings.EqualFold(cfg.Type, "gemini") || strings.EqualFold(cfg.Type, "google") {
		var options []google.Option
		if cfg.Token != "" {
			options = append(options, google.WithToken(cfg.Token))
		}
		if model.Client != nil {
			options = append(options, google.WithClient(model.Client))
		}
		if model.MaxRetries != nil {
			options = append(options, google.WithMaxRetries(*model.MaxRetries))
		}
		return google.NewDecider(cfg.URL, model.ID, options...)
	}
	if strings.EqualFold(cfg.Type, "openai") || strings.EqualFold(cfg.Type, "openai-compatible") {
		var options []openai.Option
		if cfg.Token != "" {
			options = append(options, openai.WithToken(cfg.Token))
		}
		if model.Client != nil {
			options = append(options, openai.WithClient(model.Client))
		}
		if model.MaxRetries != nil {
			options = append(options, openai.WithMaxRetries(*model.MaxRetries))
		}
		return openai.NewDecider(cfg.URL, model.ID, options...)
	}
	if !strings.EqualFold(cfg.Type, "typesafe") {
		return nil, errors.New("invalid decider type: " + cfg.Type)
	}
	var options []typesafe.Option
	if cfg.Token != "" {
		options = append(options, typesafe.WithToken(cfg.Token))
	}
	if model.Client != nil {
		options = append(options, typesafe.WithClient(model.Client))
	}
	if model.MaxRetries != nil {
		options = append(options, typesafe.WithMaxRetries(*model.MaxRetries))
	}
	return typesafe.NewDecider(cfg.URL, model.ID, options...)
}
