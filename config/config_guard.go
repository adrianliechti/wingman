package config

import (
	"errors"
	"strings"

	"net/http"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/guard/custom"
	"github.com/adrianliechti/wingman/pkg/guard/multi"
	"github.com/adrianliechti/wingman/pkg/guard/openai"
	"github.com/adrianliechti/wingman/pkg/otel"
)

func (cfg *Config) RegisterGuard(id string, p guard.Provider) {
	if cfg.guard == nil {
		cfg.guard = make(map[string]guard.Provider)
	}

	if _, ok := cfg.guard[""]; !ok {
		cfg.guard[""] = p
	}

	cfg.guard[id] = p
}

func (cfg *Config) Guard(id string) (guard.Provider, error) {
	if cfg.guard != nil {
		if g, ok := cfg.guard[id]; ok {
			return g, nil
		}
	}

	return nil, errors.New("guard not found: " + id)
}

type guardConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Model string `yaml:"model"`

	Proxy *proxyConfig `yaml:"proxy"`
}

type guardContext struct {
	Client *http.Client
}

func (cfg *Config) registerGuards(f *configFile) error {
	var configs map[string]guardConfig

	if err := f.Guards.Decode(&configs); err != nil {
		return err
	}

	var guards []guard.Provider

	for _, node := range f.Guards.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := guardContext{}

		if config.Proxy != nil {
			client, err := config.Proxy.proxyClient()

			if err != nil {
				return err
			}

			context.Client = client
		}

		guard, err := createGuard(config, context)

		if err != nil {
			return err
		}

		if _, ok := guard.(otel.Guard); !ok {
			guard = otel.NewGuard(config.Type, id, guard)
		}

		guards = append(guards, guard)

		cfg.RegisterGuard(id, guard)
	}

	if len(guards) > 0 {
		cfg.guard[""] = multi.New(guards...)
	}

	return nil
}

func createGuard(cfg guardConfig, context guardContext) (guard.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "openai":
		return openaiGuard(cfg, context)

	case "custom":
		return customGuard(cfg, context)

	default:
		return nil, errors.New("invalid guard type: " + cfg.Type)
	}
}

func openaiGuard(cfg guardConfig, context guardContext) (guard.Provider, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if cfg.Model != "" {
		options = append(options, openai.WithModel(cfg.Model))
	}

	if context.Client != nil {
		options = append(options, openai.WithClient(context.Client))
	}

	return openai.New(cfg.URL, options...)
}

func customGuard(cfg guardConfig, context guardContext) (guard.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
