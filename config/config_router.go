package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
	"github.com/adrianliechti/wingman/pkg/router/auto"
	"github.com/adrianliechti/wingman/pkg/router/roundrobin"
)

type routerConfig struct {
	Type string `yaml:"type"`

	Model string `yaml:"model"`

	Effort    string `yaml:"effort"`
	Verbosity string `yaml:"verbosity"`

	Routes []routeConfig `yaml:"routes"`
}

type routeConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	Model string `yaml:"model"`

	Options *routeOptions `yaml:"options"`
}

type routeOptions struct {
	Effort    string `yaml:"effort"`
	Verbosity string `yaml:"verbosity"`

	Temperature *float32 `yaml:"temperature"`
}

type routerContext struct {
	Routes []router.Route

	Completer provider.Completer

	Effort    string `yaml:"effort"`
	Verbosity string `yaml:"verbosity"`
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

		context := routerContext{
			Routes: []router.Route{},

			Effort:    config.Effort,
			Verbosity: config.Verbosity,
		}

		if config.Model != "" {
			completer, err := cfg.Completer(config.Model)

			if err != nil {
				return err
			}

			context.Completer = completer
		}

		for _, r := range config.Routes {
			completer, err := cfg.Completer(r.Model)

			if err != nil {
				return err
			}

			route := router.Route{
				Name:        r.Name,
				Description: r.Description,

				Completer: completer,
			}

			if r.Options != nil {
				route.Options = &router.RouteOptions{
					Effort:    provider.Effort(r.Options.Effort),
					Verbosity: provider.Verbosity(r.Options.Verbosity),
				}
			}

			context.Routes = append(context.Routes, route)
		}

		router, err := createRouter(config, context)

		if err != nil {
			return err
		}

		if completer, ok := router.(provider.Completer); ok {
			if _, ok := completer.(otel.Completer); !ok {
				completer = otel.NewCompleter(config.Type, id, completer)
			}

			cfg.RegisterCompleter(id, completer)
		}
	}

	return nil
}

func createRouter(cfg routerConfig, context routerContext) (any, error) {
	switch strings.ToLower(cfg.Type) {
	case "auto":
		return autoRouter(cfg, context)

	case "roundrobin":
		return roundrobinRouter(cfg, context)

	default:
		return nil, errors.New("invalid router type: " + cfg.Type)
	}
}

func autoRouter(cfg routerConfig, context routerContext) (any, error) {
	return auto.NewCompleter(context.Completer, context.Routes...)
}

func roundrobinRouter(cfg routerConfig, context routerContext) (any, error) {
	return roundrobin.NewCompleter(context.Routes...)
}
