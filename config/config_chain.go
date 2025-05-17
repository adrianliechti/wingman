package config

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/index"
	"github.com/adrianliechti/wingman/pkg/limiter"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/template"

	"github.com/adrianliechti/wingman/pkg/chain"
	"github.com/adrianliechti/wingman/pkg/chain/agent"
	"github.com/adrianliechti/wingman/pkg/chain/assistant"
	"github.com/adrianliechti/wingman/pkg/chain/rag"

	"github.com/adrianliechti/wingman/pkg/tool"

	"golang.org/x/time/rate"
)

func (cfg *Config) RegisterChain(id string, p chain.Provider) {
	cfg.RegisterModel(id)

	if cfg.chains == nil {
		cfg.chains = make(map[string]chain.Provider)
	}

	cfg.chains[id] = p
}

type chainConfig struct {
	Type string `yaml:"type"`

	Index string `yaml:"index"`

	Model  string `yaml:"model"`
	Effort string `yaml:"effort"`

	Template string    `yaml:"template"`
	Messages []message `yaml:"messages"`

	Tools []string `yaml:"tools"`

	Limit       *int     `yaml:"limit"`
	Temperature *float32 `yaml:"temperature"`
}

type chainContext struct {
	Model string

	Index index.Provider

	Embedder  provider.Embedder
	Completer provider.Completer

	Template *template.Template
	Messages []provider.Message

	Tools  map[string]tool.Provider
	Effort provider.ReasoningEffort

	Limiter *rate.Limiter
}

func (cfg *Config) registerChains(f *configFile) error {
	var configs map[string]chainConfig

	if err := f.Chains.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Chains.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := chainContext{
			Model: id,

			Messages: make([]provider.Message, 0),

			Tools:  make(map[string]tool.Provider),
			Effort: parseEffort(config.Effort),

			Limiter: createLimiter(config.Limit),
		}

		if config.Index != "" {
			index, err := cfg.Index(config.Index)

			if err != nil {
				return err
			}

			context.Index = index
		}

		if config.Model != "" {
			if p, err := cfg.Completer(config.Model); err == nil {
				context.Completer = p
			}

			if p, err := cfg.Embedder(config.Model); err == nil {
				context.Embedder = p
			}
		}

		for _, t := range config.Tools {
			tool, err := cfg.Tool(t)

			if err != nil {
				return err
			}

			context.Tools[t] = tool
		}

		if config.Template != "" {
			template, err := parseTemplate(config.Template)

			if err != nil {
				return err
			}

			context.Template = template
		}

		if config.Messages != nil {
			messages, err := parseMessages(config.Messages)

			if err != nil {
				return err
			}

			context.Messages = messages
		}

		chain, err := createChain(config, context)

		if err != nil {
			return err
		}

		if _, ok := chain.(limiter.Chain); !ok {
			chain = limiter.NewChain(context.Limiter, chain)
		}

		if _, ok := chain.(otel.Chain); !ok {
			chain = otel.NewChain(config.Type, id, chain)
		}

		cfg.RegisterChain(id, chain)
	}

	return nil
}

func createChain(cfg chainConfig, context chainContext) (chain.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "agent":
		return agentChain(cfg, context)

	case "assistant":
		return assistantChain(cfg, context)

	case "rag":
		return ragChain(cfg, context)

	default:
		return nil, errors.New("invalid chain type: " + cfg.Type)
	}
}

func agentChain(cfg chainConfig, context chainContext) (chain.Provider, error) {
	var options []agent.Option

	if context.Completer != nil {
		options = append(options, agent.WithCompleter(context.Completer))
	}

	if context.Tools != nil {
		options = append(options, agent.WithTools(slices.Collect(maps.Values(context.Tools))...))
	}

	if context.Messages != nil {
		options = append(options, agent.WithMessages(context.Messages...))
	}

	if context.Effort != "" {
		options = append(options, agent.WithEffort(context.Effort))
	}

	return agent.New(context.Model, options...)
}

func assistantChain(cfg chainConfig, context chainContext) (chain.Provider, error) {
	var options []assistant.Option

	if context.Completer != nil {
		options = append(options, assistant.WithCompleter(context.Completer))
	}

	if context.Messages != nil {
		options = append(options, assistant.WithMessages(context.Messages...))
	}

	if context.Effort != "" {
		options = append(options, assistant.WithEffort(context.Effort))
	}

	if cfg.Temperature != nil {
		options = append(options, assistant.WithTemperature(*cfg.Temperature))
	}

	return assistant.New(options...)
}

func ragChain(cfg chainConfig, context chainContext) (chain.Provider, error) {
	var options []rag.Option

	if context.Completer != nil {
		options = append(options, rag.WithCompleter(context.Completer))
	}

	if context.Template != nil {
		options = append(options, rag.WithTemplate(context.Template))
	}

	if context.Messages != nil {
		options = append(options, rag.WithMessages(context.Messages...))
	}

	if context.Index != nil {
		options = append(options, rag.WithIndex(context.Index))
	}

	if context.Effort != "" {
		options = append(options, rag.WithEffort(context.Effort))
	}

	if cfg.Temperature != nil {
		options = append(options, rag.WithTemperature(*cfg.Temperature))
	}

	return rag.New(options...)
}
