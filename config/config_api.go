package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/api"
	"github.com/adrianliechti/wingman/pkg/api/json"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func (cfg *Config) RegisterAPI(id string, p api.Provider) {
	if cfg.APIs == nil {
		cfg.APIs = make(map[string]api.Provider)
	}

	cfg.APIs[id] = p
}

type apiConfig struct {
	Type string `yaml:"type"`

	Model string `yaml:"model"`

	Effort    string `yaml:"effort"`
	Verbosity string `yaml:"verbosity"`

	InputSchema  *apiSchema `yaml:"input"`
	OutputSchema *apiSchema `yaml:"output"`
}

type apiSchema struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	Schema map[string]any `yaml:"schema"`
}

type apiContext struct {
	Completer provider.Completer

	Effort    provider.Effort
	Verbosity provider.Verbosity

	InputSchema  *api.Schema
	OutputSchema *api.Schema
}

func (cfg *Config) registerAPI(f *configFile) error {
	var configs map[string]apiConfig

	if err := f.APIs.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.APIs.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := apiContext{
			Effort:    provider.Effort(config.Effort),
			Verbosity: provider.Verbosity(config.Verbosity),
		}

		if config.Model != "" {
			if p, err := cfg.Completer(config.Model); err == nil {
				context.Completer = p
			}
		}

		if config.InputSchema != nil {
			context.InputSchema = &api.Schema{
				Name:        config.InputSchema.Name,
				Description: config.InputSchema.Description,

				Schema: config.InputSchema.Schema,
			}
		}

		if config.OutputSchema != nil {
			context.OutputSchema = &api.Schema{
				Name:        config.OutputSchema.Name,
				Description: config.OutputSchema.Description,

				Schema: config.OutputSchema.Schema,
			}
		}

		api, err := createAPI(config, context)

		if err != nil {
			return err
		}

		cfg.RegisterAPI(id, api)
	}

	return nil
}

func createAPI(cfg apiConfig, context apiContext) (http.Handler, error) {
	switch strings.ToLower(cfg.Type) {
	case "json":
		return jsonAPI(cfg, context)

	default:
		return nil, errors.New("invalid api type: " + cfg.Type)
	}
}

func jsonAPI(cfg apiConfig, context apiContext) (api.Provider, error) {
	var options []json.Option

	if context.Completer != nil {
		options = append(options, json.WithCompleter(context.Completer))
	}

	if context.InputSchema != nil {
		options = append(options, json.WithInputSchema(*context.InputSchema))
	}

	if context.OutputSchema != nil {
		options = append(options, json.WithOutputSchema(*context.OutputSchema))
	}

	return json.New(options...)
}
