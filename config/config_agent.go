package config

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/agent/assistant"
	"github.com/adrianliechti/wingman/pkg/agent/react"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/template"
	"github.com/adrianliechti/wingman/pkg/tool"
)

func (cfg *Config) RegisterAgent(id string, p provider.Completer) {
	cfg.RegisterModel(id)

	if cfg.agents == nil {
		cfg.agents = make(map[string]provider.Completer)
	}

	cfg.agents[id] = p
}

type agentConfig struct {
	Type string `yaml:"type"`

	Model string `yaml:"model"`

	Template string    `yaml:"template"`
	Messages []message `yaml:"messages"`

	Tools []string `yaml:"tools"`

	Effort    string `yaml:"effort"`
	Verbosity string `yaml:"verbosity"`

	Temperature *float32 `yaml:"temperature"`
}

type agentContext struct {
	Completer provider.Completer

	Template *template.Template
	Messages []provider.Message

	Tools map[string]tool.Provider

	Effort    provider.Effort
	Verbosity provider.Verbosity
}

func (cfg *Config) registerAgents(f *configFile) error {
	var configs map[string]agentConfig

	if err := f.Agents.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Agents.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := agentContext{
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

		a, err := createAgent(config, context)

		if err != nil {
			return err
		}

		a = otel.NewCompleterSpan("agent "+id, a)

		cfg.RegisterAgent(id, a)
	}

	return nil
}

func createAgent(cfg agentConfig, context agentContext) (provider.Completer, error) {
	switch strings.ToLower(cfg.Type) {
	case "react":
		return reactAgent(cfg, context)

	case "assistant":
		return assistantAgent(cfg, context)

	default:
		return nil, errors.New("invalid agent type: " + cfg.Type)
	}
}

func reactAgent(cfg agentConfig, context agentContext) (provider.Completer, error) {
	var options []react.Option

	if context.Completer != nil {
		options = append(options, react.WithCompleter(context.Completer))
	}

	options = append(options, react.WithTools(slices.Collect(maps.Values(context.Tools))...))

	if context.Messages != nil {
		options = append(options, react.WithMessages(context.Messages...))
	}

	if context.Effort != "" {
		options = append(options, react.WithEffort(context.Effort))
	}

	if context.Verbosity != "" {
		options = append(options, react.WithVerbosity(context.Verbosity))
	}

	return react.New(cfg.Model, options...)
}

func assistantAgent(cfg agentConfig, context agentContext) (provider.Completer, error) {
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

	if context.Verbosity != "" {
		options = append(options, assistant.WithVerbosity(context.Verbosity))
	}

	return assistant.New(cfg.Model, options...)
}
