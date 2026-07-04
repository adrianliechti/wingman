package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
	"github.com/adrianliechti/wingman/pkg/router/adaptive"
	"github.com/adrianliechti/wingman/pkg/router/classifier"
	"github.com/adrianliechti/wingman/pkg/router/roundrobin"
)

type routerConfig struct {
	Type string `yaml:"type"`

	Models   []string `yaml:"models"`
	Fallback string   `yaml:"fallback"`

	// FirstTokenTimeout bounds the wait for the first response token before
	// failing over to another provider (e.g. "30s"). Defaults to 2m
	FirstTokenTimeout string `yaml:"first_token_timeout"`

	// FailureThreshold is the number of consecutive failures that open a
	// provider's circuit. Defaults to 5
	FailureThreshold int `yaml:"failure_threshold"`

	// RecoveryTimeout is how long an open circuit waits before allowing a
	// probe request (e.g. "1m"). Defaults to 30s
	RecoveryTimeout string `yaml:"recovery_timeout"`

	// Candidates lists the per-task routing options for type "classifier".
	Candidates []routerCandidateConfig `yaml:"candidates"`

	// Default is the classifier's universal fail-safe candidate (a candidate
	// model id). It must match one of the candidates.
	Default string `yaml:"default"`

	// Embedder is the model id of an embedder (resolved via cfg.Embedder)
	// enabling the classifier's embedding-similarity tier. Omit to keep routing
	// purely local.
	Embedder string `yaml:"embedder"`

	// Margin is the minimum cosine-similarity advantage the best candidate
	// must hold over the runner-up for the embedding tier to resolve a pick
	// (default 0.05). Only used when Embedder is set.
	Margin float64 `yaml:"margin"`

	// Completer is the model id of a completer (resolved via cfg.Completer, like
	// "model" elsewhere). The classifier uses it as the optional LLM-as-judge
	// tier; omit to keep it off (the default).
	Completer string `yaml:"completer"`
}

// routerCandidateConfig describes one classifier candidate. Model is a completer
// model id (resolved via cfg.Completer, like "model" elsewhere). Cost is only a
// tie-breaker among candidates that already clear the difficulty bar.
type routerCandidateConfig struct {
	Model string `yaml:"model"`
	Card  string `yaml:"card"`

	Cost          float64 `yaml:"cost"`
	MaxDifficulty int     `yaml:"max_difficulty"`
	Vision        bool    `yaml:"vision"`
	MaxContext    int     `yaml:"max_context"`

	Examples []string `yaml:"examples"`
}

type routerContext struct {
	Completers []provider.Completer
	Fallback   provider.Completer
}

func (cfg *Config) registerRouters(f *configFile) error {
	var configs map[string]routerConfig

	if err := decodeStrict(&f.Routers, &configs); err != nil {
		return err
	}

	for _, node := range f.Routers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		if strings.ToLower(config.Type) == "classifier" {
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

		completer, err := createRouter(config, context)

		if err != nil {
			return err
		}

		cfg.RegisterCompleter(id, otel.NewCompleterSpan("router "+id, completer))
	}

	// Classifiers register last, so their candidates can reference sibling
	// routers (e.g. an adaptive load-balancer as a candidate) regardless of
	// document order.
	for _, node := range f.Routers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok || strings.ToLower(config.Type) != "classifier" {
			continue
		}

		completer, err := cfg.createClassifier(config)

		if err != nil {
			return err
		}

		cfg.RegisterCompleter(id, otel.NewCompleterSpan("router "+id, completer))
	}

	return nil
}

func (cfg *Config) createClassifier(config routerConfig) (provider.Completer, error) {
	if len(config.Candidates) == 0 {
		return nil, errors.New("classifier router requires candidates")
	}

	candidates := make([]classifier.Candidate, 0, len(config.Candidates))

	defaultIndex := 0
	defaultFound := config.Default == ""

	for i, cc := range config.Candidates {
		if cc.Model == "" {
			return nil, errors.New("classifier candidate requires a model")
		}

		completer, err := cfg.Completer(cc.Model)

		if err != nil {
			return nil, err
		}

		candidates = append(candidates, classifier.Candidate{
			Completer: completer,

			Model: cc.Model,
			Card:  cc.Card,

			Cost:          cc.Cost,
			MaxDifficulty: cc.MaxDifficulty,
			Vision:        cc.Vision,
			MaxContext:    cc.MaxContext,

			Examples: cc.Examples,
		})

		if cc.Model == config.Default {
			defaultIndex = i
			defaultFound = true
		}
	}

	if !defaultFound {
		return nil, errors.New("classifier default not found among candidates: " + config.Default)
	}

	options := classifier.Options{
		Margin:       config.Margin,
		DefaultIndex: defaultIndex,
	}

	if config.Embedder != "" {
		embedder, err := cfg.Embedder(config.Embedder)

		if err != nil {
			return nil, err
		}

		options.Embedder = embedder
	}

	if config.Completer != "" {
		judge, err := cfg.Completer(config.Completer)

		if err != nil {
			return nil, err
		}

		options.Judge = judge
	}

	return classifier.NewCompleter(candidates, options)
}

func createRouter(cfg routerConfig, context routerContext) (provider.Completer, error) {
	options, err := routerOptions(cfg, context)

	if err != nil {
		return nil, err
	}

	switch strings.ToLower(cfg.Type) {
	case "roundrobin":
		return roundrobin.NewCompleter(context.Completers, options...)

	case "adaptive":
		return adaptive.NewCompleter(context.Completers, options...)

	default:
		return nil, errors.New("invalid router type: " + cfg.Type)
	}
}

func routerOptions(cfg routerConfig, context routerContext) ([]router.Option, error) {
	var options []router.Option

	if context.Fallback != nil {
		options = append(options, router.WithFallback(context.Fallback))
	}

	if cfg.FirstTokenTimeout != "" {
		timeout, err := parseTimeout("first_token_timeout", cfg.FirstTokenTimeout)

		if err != nil {
			return nil, err
		}

		options = append(options, router.WithFirstTokenTimeout(timeout))
	}

	if cfg.FailureThreshold < 0 {
		return nil, errors.New("invalid failure_threshold: must not be negative")
	}

	if cfg.FailureThreshold > 0 {
		options = append(options, router.WithFailureThreshold(cfg.FailureThreshold))
	}

	if cfg.RecoveryTimeout != "" {
		timeout, err := parseTimeout("recovery_timeout", cfg.RecoveryTimeout)

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
