package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/retriever"
	"github.com/adrianliechti/wingman/pkg/retriever/custom"
	"github.com/adrianliechti/wingman/pkg/retriever/duckduckgo"
	"github.com/adrianliechti/wingman/pkg/retriever/exa"
	"github.com/adrianliechti/wingman/pkg/retriever/tavily"
)

func (cfg *Config) RegisterRetriever(id string, p retriever.Provider) {
	if cfg.retrievers == nil {
		cfg.retrievers = make(map[string]retriever.Provider)
	}

	if _, ok := cfg.retrievers[""]; !ok {
		cfg.retrievers[""] = p
	}

	cfg.retrievers[id] = p
}

func (cfg *Config) Retriever(id string) (retriever.Provider, error) {
	if cfg.retrievers != nil {
		if i, ok := cfg.retrievers[id]; ok {
			return i, nil
		}
	}

	return nil, errors.New("retriever not found: " + id)
}

type retrieverConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`
}

type retrieverContext struct {
	Client *http.Client
}

func (cfg *Config) registerRetrievers(f *configFile) error {
	var configs map[string]retrieverConfig

	if err := f.Retrievers.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Retrievers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := retrieverContext{}

		if config.Proxy != nil {
			client, err := config.Proxy.proxyClient()

			if err != nil {
				return err
			}

			context.Client = client
		}

		index, err := createRetriever(config, context)

		if err != nil {
			return err
		}

		if _, ok := index.(otel.Retriever); !ok {
			index = otel.NewRetriever(config.Type, id, index)
		}

		cfg.RegisterRetriever(id, index)
	}

	return nil
}

func createRetriever(cfg retrieverConfig, context retrieverContext) (retriever.Provider, error) {
	switch strings.ToLower(cfg.Type) {

	case "duckduckgo":
		return duckduckgoRetriever(cfg, context)

	case "exa":
		return exaRetriever(cfg, context)

	case "tavily":
		return tavilyRetriever(cfg, context)

	case "custom":
		return customIndex(cfg)

	default:
		return nil, errors.New("invalid index type: " + cfg.Type)
	}
}

func duckduckgoRetriever(cfg retrieverConfig, context retrieverContext) (retriever.Provider, error) {
	var options []duckduckgo.Option

	if context.Client != nil {
		options = append(options, duckduckgo.WithClient(context.Client))
	}

	return duckduckgo.New(options...)
}

func exaRetriever(cfg retrieverConfig, context retrieverContext) (retriever.Provider, error) {
	var options []exa.Option

	if context.Client != nil {
		options = append(options, exa.WithClient(context.Client))
	}

	return exa.New(cfg.Token, options...)
}

func tavilyRetriever(cfg retrieverConfig, context retrieverContext) (retriever.Provider, error) {
	var options []tavily.Option

	if context.Client != nil {
		options = append(options, tavily.WithClient(context.Client))
	}

	return tavily.New(cfg.Token, options...)
}

func customIndex(cfg retrieverConfig) (*custom.Client, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
