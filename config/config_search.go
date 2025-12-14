package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/searcher/custom"
	"github.com/adrianliechti/wingman/pkg/searcher/duckduckgo"
	"github.com/adrianliechti/wingman/pkg/searcher/exa"
	"github.com/adrianliechti/wingman/pkg/searcher/tavily"
	"golang.org/x/time/rate"
)

func (cfg *Config) RegisterSearcher(id string, p searcher.Provider) {
	if cfg.searcher == nil {
		cfg.searcher = make(map[string]searcher.Provider)
	}

	if _, ok := cfg.searcher[""]; !ok {
		cfg.searcher[""] = p
	}

	cfg.searcher[id] = p
}

func (cfg *Config) Searcher(id string) (searcher.Provider, error) {
	if cfg.searcher != nil {
		if i, ok := cfg.searcher[id]; ok {
			return i, nil
		}
	}

	return nil, errors.New("searcher not found: " + id)
}

type searcherConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Limit *int `yaml:"limit"`
}

type searcherContext struct {
	Client  *http.Client
	Limiter *rate.Limiter
}

func (cfg *Config) registerSearchers(f *configFile) error {
	var configs map[string]searcherConfig

	if err := f.Searchers.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Searchers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := searcherContext{
			Limiter: createLimiter(config.Limit),
		}

		if config.Proxy != nil {
			client, err := config.Proxy.proxyClient()

			if err != nil {
				return err
			}

			context.Client = client
		}

		index, err := createSearcher(config, context)

		if err != nil {
			return err
		}

		if _, ok := index.(otel.Searcher); !ok {
			index = otel.NewSearcher(config.Type, id, index)
		}

		cfg.RegisterSearcher(id, index)
	}

	return nil
}

func createSearcher(cfg searcherConfig, context searcherContext) (searcher.Provider, error) {
	switch strings.ToLower(cfg.Type) {

	case "duckduckgo":
		return duckduckgoSearch(cfg, context)

	case "exa":
		return exaSearch(cfg, context)

	case "tavily":
		return tavilySearch(cfg, context)

	case "custom", "wingman-searcher":
		return customSearcher(cfg)

	default:
		return nil, errors.New("invalid search type: " + cfg.Type)
	}
}

func duckduckgoSearch(cfg searcherConfig, context searcherContext) (searcher.Provider, error) {
	var options []duckduckgo.Option

	if context.Client != nil {
		options = append(options, duckduckgo.WithClient(context.Client))
	}

	return duckduckgo.New(options...)
}

func exaSearch(cfg searcherConfig, context searcherContext) (searcher.Provider, error) {
	var options []exa.Option

	if context.Client != nil {
		options = append(options, exa.WithClient(context.Client))
	}

	return exa.New(cfg.Token, options...)
}

func tavilySearch(cfg searcherConfig, context searcherContext) (searcher.Provider, error) {
	var options []tavily.Option

	if context.Client != nil {
		options = append(options, tavily.WithClient(context.Client))
	}

	return tavily.New(cfg.Token, options...)
}

func customSearcher(cfg searcherConfig) (searcher.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
