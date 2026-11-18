package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/router"
	"github.com/adrianliechti/wingman/pkg/router/adaptive"
	"github.com/adrianliechti/wingman/pkg/router/auto"
	"github.com/adrianliechti/wingman/pkg/router/roundrobin"
)

type routerConfig struct {
	Type string `yaml:"type"`

	Models   []string `yaml:"models"`
	Fallback string   `yaml:"fallback"`

	// ReasoningSignatures set to false strips reasoning and compaction
	// signatures before routing. By default each member replays only the
	// signatures it produced itself, so thinking continues whenever the
	// router lands on the same member again.
	ReasoningSignatures *bool `yaml:"reasoning_signatures"`

	// FirstTokenTimeout bounds the wait for the first response token before
	// failing over to another provider (e.g. "30s"). Defaults to 2m
	FirstTokenTimeout string `yaml:"first_token_timeout"`

	// FailureThreshold is the number of consecutive failures that open a
	// provider's circuit. Defaults to 5
	FailureThreshold int `yaml:"failure_threshold"`

	// RecoveryTimeout is how long an open circuit waits before allowing a
	// probe request (e.g. "1m"). Defaults to 30s
	RecoveryTimeout string `yaml:"recovery_timeout"`

	// Auto routers ask a decision model to choose among the described routes.
	Decider string `yaml:"decider"`
	Routes  []struct {
		Model string `yaml:"model"`
		When  string `yaml:"when"`
	} `yaml:"routes"`
}

func (cfg *Config) registerRouters(f *configFile) error {
	var configs map[string]routerConfig
	if err := decodeStrict(&f.Routers, &configs); err != nil {
		return err
	}

	// A router can route to another router or fall back on one. Register each
	// after the routers it names, whatever their order in the configuration.
	ids := configIDs(&f.Routers)
	waiting := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, ok := configs[id]; ok {
			waiting[id] = true
		}
	}
	for len(waiting) > 0 {
		registered := false
		for _, id := range ids {
			config := configs[id]
			if !waiting[id] || slices.ContainsFunc(config.targets(), func(target string) bool { return target != id && waiting[target] }) {
				continue
			}
			completer, err := cfg.createRouter(config)
			if err != nil {
				return fmt.Errorf("router %q: %w", id, err)
			}
			if config.ReasoningSignatures != nil && !*config.ReasoningSignatures {
				completer = signatures.FromCompleter(completer)
			}
			cfg.RegisterCompleter(id, otel.NewCompleterSpan("router "+id, completer))
			delete(waiting, id)
			registered = true
		}
		if !registered {
			return fmt.Errorf("routers reference each other: %s", strings.Join(slices.Sorted(maps.Keys(waiting)), ", "))
		}
	}
	return nil
}

// targets lists every model the router can send a request to.
func (config routerConfig) targets() []string {
	targets := append(slices.Clone(config.Models), config.Fallback)
	for _, route := range config.Routes {
		targets = append(targets, route.Model)
	}
	return targets
}

func (cfg *Config) createRouter(config routerConfig) (provider.Completer, error) {
	options, err := cfg.routerOptions(config)
	if err != nil {
		return nil, err
	}

	if strings.EqualFold(config.Type, "auto") {
		if len(config.Models) > 0 {
			return nil, errors.New("auto router uses routes to specify its models")
		}
		_, decider, err := cfg.Decider(config.Decider)
		if err != nil {
			return nil, err
		}
		var routes []auto.Route
		for _, r := range config.Routes {
			completer, err := cfg.Completer(r.Model)
			if err != nil {
				return nil, err
			}
			routes = append(routes, auto.Route{Model: r.Model, When: r.When, Completer: completer})
		}
		return auto.NewCompleter(routes, decider, options...)
	}

	if len(config.Routes) > 0 || config.Decider != "" {
		return nil, errors.New("routes and decider require router type auto")
	}
	var members []provider.Completer
	for _, model := range config.Models {
		completer, err := cfg.Completer(model)
		if err != nil {
			return nil, err
		}
		members = append(members, completer)
	}
	switch strings.ToLower(config.Type) {
	case "roundrobin":
		return roundrobin.NewCompleter(members, options...)
	case "adaptive":
		return adaptive.NewCompleter(members, options...)
	default:
		return nil, errors.New("invalid router type: " + config.Type)
	}
}

func (cfg *Config) routerOptions(config routerConfig) ([]router.Option, error) {
	var options []router.Option

	if config.Fallback != "" {
		fallback, err := cfg.Completer(config.Fallback)
		if err != nil {
			return nil, err
		}
		options = append(options, router.WithFallback(fallback))
	}

	if config.FirstTokenTimeout != "" {
		timeout, err := parseTimeout("first_token_timeout", config.FirstTokenTimeout)

		if err != nil {
			return nil, err
		}

		options = append(options, router.WithFirstTokenTimeout(timeout))
	}

	if config.FailureThreshold < 0 {
		return nil, errors.New("invalid failure_threshold: must not be negative")
	}

	if config.FailureThreshold > 0 {
		options = append(options, router.WithFailureThreshold(config.FailureThreshold))
	}

	if config.RecoveryTimeout != "" {
		timeout, err := parseTimeout("recovery_timeout", config.RecoveryTimeout)

		if err != nil {
			return nil, err
		}

		options = append(options, router.WithRecoveryTimeout(timeout))
	}

	return options, nil
}

func parseTimeout(name, value string) (time.Duration, error) {
	timeout, err := time.ParseDuration(value)

	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}

	if timeout < 0 {
		return 0, fmt.Errorf("invalid %s: must not be negative", name)
	}

	return timeout, nil
}
