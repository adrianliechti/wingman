package config

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/trimmer"
	"github.com/adrianliechti/wingman/pkg/template"

	"github.com/adrianliechti/wingman/pkg/chain"
	"github.com/adrianliechti/wingman/pkg/chain/agent"

	"github.com/adrianliechti/wingman/pkg/tool"
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

	Model string `yaml:"model"`

	Template string    `yaml:"template"`
	Messages []message `yaml:"messages"`

	Tools []string `yaml:"tools"`

	Effort    string `yaml:"effort"`
	Verbosity string `yaml:"verbosity"`

	Temperature *float32 `yaml:"temperature"`

	Compaction string `yaml:"compaction"`
}

type chainContext struct {
	Completer provider.Completer

	Template *template.Template
	Messages []provider.Message

	Tools map[string]tool.Provider

	Effort    provider.Effort
	Verbosity provider.Verbosity
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
			Messages: make([]provider.Message, 0),

			Tools: make(map[string]tool.Provider),

			Effort:    provider.Effort(config.Effort),
			Verbosity: provider.Verbosity(config.Verbosity),
		}

		if config.Model != "" {
			if p, err := cfg.Completer(config.Model); err == nil {
				context.Completer = p
			}
		}

		if context.Completer != nil {
			switch strings.ToLower(config.Compaction) {
			case "":
				// no compaction

			case "trim":
				context.Completer = trimmer.New(context.Completer)

			default:
				return errors.New("invalid compaction type: " + config.Compaction)
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

		cfg.RegisterChain(id, chain)
	}

	return nil
}

func createChain(cfg chainConfig, context chainContext) (chain.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "agent", "assistant":
		return agentChain(cfg, context)

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

	if context.Verbosity != "" {
		options = append(options, agent.WithVerbosity(context.Verbosity))
	}

	return agent.New(cfg.Model, options...)
}
