package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/researcher/anthropic"
	"github.com/adrianliechti/wingman/pkg/researcher/custom"
	"github.com/adrianliechti/wingman/pkg/researcher/exa"
	"github.com/adrianliechti/wingman/pkg/researcher/llm"
	"github.com/adrianliechti/wingman/pkg/researcher/openai"
	"github.com/adrianliechti/wingman/pkg/researcher/perplexity"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"golang.org/x/time/rate"
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

	return nil, errors.New("researcher not found: " + id)
}

type researcherConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Model string `yaml:"model"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Scraper  string `yaml:"scraper"`
	Searcher string `yaml:"searcher"`

	Effort    string `yaml:"effort"`
	Verbosity string `yaml:"verbosity"`

	Limit *int `yaml:"limit"`
}

type researcherContext struct {
	Client *http.Client

	Completer provider.Completer

	Scraper  scraper.Provider
	Searcher searcher.Provider

	Effort    provider.Effort
	Verbosity provider.Verbosity

	Limiter *rate.Limiter
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

		context := researcherContext{
			Effort:    provider.Effort(config.Effort),
			Verbosity: provider.Verbosity(config.Verbosity),

			Limiter: createLimiter(config.Limit),
		}

		if p, err := cfg.Completer(config.Model); err == nil {
			context.Completer = p
		}

		if config.Scraper != "" {
			if p, err := cfg.Scraper(config.Scraper); err == nil {
				context.Scraper = p
			}
		}

		if config.Searcher != "" {
			if p, err := cfg.Searcher(config.Searcher); err == nil {
				context.Searcher = p
			}
		}

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

		if _, ok := index.(otel.Researcher); !ok {
			index = otel.NewResearcher(config.Type, id, index)
		}

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

	case "llm":
		return llmResearcher(cfg, context)

	case "openai":
		return openaiResearcher(cfg, context)

	case "perplexity":
		return perplexityResearcher(cfg, context)

	case "custom", "wingman-researcher":
		return customResearcher(cfg)

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

func llmResearcher(cfg researcherConfig, context researcherContext) (researcher.Provider, error) {
	var options []llm.Option

	if context.Scraper != nil {
		options = append(options, llm.WithScraper(context.Scraper))
	}

	if context.Effort != "" {
		options = append(options, llm.WithEffort(context.Effort))
	}

	if context.Verbosity != "" {
		options = append(options, llm.WithVerbosity(context.Verbosity))
	}

	return llm.New(context.Completer, context.Searcher, options...)
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

func customResearcher(cfg researcherConfig) (researcher.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
