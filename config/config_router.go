package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router/adaptive"
	"github.com/adrianliechti/wingman/pkg/router/roundrobin"
)

type routerConfig struct {
	Type string `yaml:"type"`

	Models   []string `yaml:"models"`
	Fallback string   `yaml:"fallback"`
}

type routerContext struct {
	Completers []provider.Completer
	Fallback   provider.Completer
}

func (cfg *Config) registerRouters(f *configFile) error {
	var configs map[string]routerConfig

	if err := f.Routers.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Routers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := routerContext{}

		for _, m := range config.Models {
			completer, err := cfg.Completer(m)

			if err != nil {
				return err
			}

			context.Completers = append(context.Completers, completer)
		}

		if config.Fallback != "" {
			fallback, err := cfg.Completer(config.Fallback)

			if err != nil {
				return err
			}

			context.Fallback = fallback
		}

		router, err := createRouter(config, context)

		if err != nil {
			return err
		}

		if completer, ok := router.(provider.Completer); ok {
			cfg.RegisterCompleter(id, completer)
		}
	}

	return nil
}

func createRouter(cfg routerConfig, context routerContext) (any, error) {
	switch strings.ToLower(cfg.Type) {
	case "roundrobin":
		return roundrobinRouter(cfg, context)

	case "adaptive":
		return adaptiveRouter(cfg, context)

	default:
		return nil, errors.New("invalid router type: " + cfg.Type)
	}
}

func roundrobinRouter(cfg routerConfig, context routerContext) (any, error) {
	var options []roundrobin.Option

	if context.Fallback != nil {
		options = append(options, roundrobin.WithFallback(context.Fallback))
	}

	return roundrobin.NewCompleter(context.Completers, options...)
}

func adaptiveRouter(cfg routerConfig, context routerContext) (any, error) {
	var options []adaptive.Option

	if context.Fallback != nil {
		options = append(options, adaptive.WithFallback(context.Fallback))
	}

	return adaptive.NewCompleter(context.Completers, options...)
}
