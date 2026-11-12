package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/decider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/provider/typesafe"
)

type registeredDecider struct {
	provider.Decider
	id     string
	native bool
}

func (cfg *Config) RegisterDecider(id string, p provider.Decider) {
	cfg.registerDecider(id, p, true)
}

// RegisterDecisionAdapter enables a completion or embedding adapter for decisions.
// Native deciders always take priority when selecting a default model.
func (cfg *Config) RegisterDecisionAdapter(id string, p provider.Decider) {
	cfg.registerDecider(id, p, false)
}

func (cfg *Config) registerDecider(id string, p provider.Decider, native bool) {
	cfg.RegisterModel(id)
	if cfg.decider == nil {
		cfg.decider = make(map[string]registeredDecider)
	}
	entry := registeredDecider{Decider: p, id: id, native: native}
	if current, ok := cfg.decider[""]; !ok || native && !current.native {
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

type deciderConfig struct {
	Model string `yaml:"model"`

	Effort string `yaml:"effort"`
}

func (cfg *Config) registerDeciders(f *configFile) error {
	var configs map[string]deciderConfig
	if err := decodeStrict(&f.Deciders, &configs); err != nil {
		return err
	}
	for _, id := range configIDs(&f.Deciders) {
		config := configs[id]
		model := config.Model
		if strings.TrimSpace(id) == "" || strings.TrimSpace(model) == "" {
			return errors.New("decision adapter requires an alias and model")
		}
		if _, exists := cfg.decider[id]; exists {
			return errors.New("decision alias already registered: " + id)
		}
		if p, err := cfg.Completer(model); err == nil {
			options, err := config.completerOptions()
			if err != nil {
				return err
			}
			cfg.RegisterDecisionAdapter(id, decider.FromCompleter(model, p, options...))
			continue
		}
		if p, err := cfg.Embedder(model); err == nil {
			if config.Effort != "" {
				return errors.New("effort requires a completion-backed decider: " + id)
			}
			cfg.RegisterDecisionAdapter(id, decider.FromEmbedder(model, p))
			continue
		}
		return errors.New("decision adapter requires a configured completer or embedder: " + model)
	}
	return nil
}

func (c deciderConfig) completerOptions() ([]decider.Option, error) {
	var options []decider.Option
	if c.Effort != "" {
		switch provider.Effort(c.Effort) {
		case "none", provider.EffortMinimal, provider.EffortLow, provider.EffortMedium, provider.EffortHigh, provider.EffortXHigh, provider.EffortMax:
			options = append(options, decider.WithEffort(provider.Effort(c.Effort)))
		default:
			return nil, errors.New("invalid decider effort: " + c.Effort)
		}
	}
	return options, nil
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
