package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/researcher/anthropic"
	"github.com/adrianliechti/wingman/pkg/researcher/exa"
	"github.com/adrianliechti/wingman/pkg/researcher/openai"
	"github.com/adrianliechti/wingman/pkg/researcher/perplexity"
)

func (cfg *Config) RegisterResearcher(id string, p researcher.Provider) {
	if cfg.researcher == nil {
		cfg.researcher = make(map[string]researcher.Provider)
	}

	if _, ok := cfg.researcher[""]; !ok {
		cfg.researcher[""] = p
	}

	cfg.researcher[id] = p
}

func (cfg *Config) Researcher(id string) (researcher.Provider, error) {
	if cfg.researcher != nil {
		if i, ok := cfg.researcher[id]; ok {
			return i, nil
		}
	}

	return nil, errors.New("retriever not found: " + id)
}

type researcherConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`
}

type researcherContext struct {
	Client *http.Client
}

func (cfg *Config) registerResearchers(f *configFile) error {
	var configs map[string]researcherConfig

	if err := f.Researchers.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Researchers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := researcherContext{}

		if config.Proxy != nil {
			client, err := config.Proxy.proxyClient()

			if err != nil {
				return err
			}

			context.Client = client
		}

		index, err := createResearcher(config, context)

		if err != nil {
			return err
		}

		// if _, ok := index.(otel.Retriever); !ok {
		// 	index = otel.NewRetriever(config.Type, id, index)
		// }

		cfg.RegisterResearcher(id, index)
	}

	return nil
}

func createResearcher(cfg researcherConfig, context researcherContext) (researcher.Provider, error) {
	switch strings.ToLower(cfg.Type) {

	case "anthropic":
		return anthropicResearcher(cfg, context)

	case "exa":
		return exaResearcher(cfg, context)

	case "openai":
		return openaiResearcher(cfg, context)

	case "perplexity":
		return perplexityResearcher(cfg, context)

	default:
		return nil, errors.New("invalid researcher type: " + cfg.Type)
	}
}

func anthropicResearcher(cfg researcherConfig, context researcherContext) (researcher.Provider, error) {
	var options []anthropic.Option

	if context.Client != nil {
		options = append(options, anthropic.WithClient(context.Client))
	}

	return anthropic.New(cfg.Token, options...)
}

func exaResearcher(cfg researcherConfig, context researcherContext) (researcher.Provider, error) {
	var options []exa.Option

	if context.Client != nil {
		options = append(options, exa.WithClient(context.Client))
	}

	return exa.New(cfg.Token, options...)
}

func openaiResearcher(cfg researcherConfig, context researcherContext) (researcher.Provider, error) {
	var options []openai.Option

	if context.Client != nil {
		options = append(options, openai.WithClient(context.Client))
	}

	return openai.New(cfg.Token, options...)
}

func perplexityResearcher(cfg researcherConfig, context researcherContext) (researcher.Provider, error) {
	var options []perplexity.Option

	if context.Client != nil {
		options = append(options, perplexity.WithClient(context.Client))
	}

	return perplexity.New(cfg.Token, options...)
}
