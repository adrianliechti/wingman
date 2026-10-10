package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/tool/custom"
	"github.com/adrianliechti/wingman/pkg/tool/mcp"
	"github.com/adrianliechti/wingman/pkg/tool/research"
	"github.com/adrianliechti/wingman/pkg/tool/scrape"
	"github.com/adrianliechti/wingman/pkg/tool/search"
	"github.com/adrianliechti/wingman/pkg/tool/translate"

	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/translator"

	"github.com/adrianliechti/wingman/pkg/otel"
)

func (cfg *Config) RegisterTool(id string, p tool.Provider) {
	if cfg.tools == nil {
		cfg.tools = make(map[string]tool.Provider)
	}

	cfg.tools[id] = p
}

func (cfg *Config) Tools() []tool.Provider {
	var tools []tool.Provider

	if cfg.tools != nil {
		for _, p := range cfg.tools {
			tools = append(tools, p)
		}
	}

	return tools
}

func (cfg *Config) Tool(id string) (tool.Provider, error) {
	if cfg.tools != nil {
		if p, ok := cfg.tools[id]; ok {
			return p, nil
		}
	}

	return nil, errors.New("tool not found: " + id)
}

type toolConfig struct {
	Type string `yaml:"type"`

	URL string `yaml:"url"`

	Vars  map[string]string `yaml:"vars"`
	Auth  *authConfig       `yaml:"auth"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Translator string `yaml:"translator"`

	Scraper    string `yaml:"scraper"`
	Searcher   string `yaml:"searcher"`
	Researcher string `yaml:"researcher"`
}

type toolContext struct {
	Client *http.Client

	Translator translator.Provider

	Scraper    scraper.Provider
	Searcher   searcher.Provider
	Researcher researcher.Provider
}

func (cfg *Config) registerTools(f *configFile) error {
	var configs map[string]toolConfig

	if err := decodeStrict(&f.Tools, &configs); err != nil {
		return err
	}

	for _, id := range configIDs(&f.Tools) {

		config, ok := configs[id]

		if !ok {
			continue
		}

		context := toolContext{}

		var err error
		switch strings.ToLower(config.Type) {
		case "scraper", "crawler":
			context.Scraper, err = cfg.Scraper(config.Scraper)
		case "search":
			context.Searcher, err = cfg.Searcher(config.Searcher)
		case "research":
			context.Researcher, err = cfg.Researcher(config.Researcher)
		case "translator":
			context.Translator, err = cfg.Translator(config.Translator)
		case "mcp":
			if config.Proxy != nil {
				context.Client, err = config.Proxy.proxyClient()
			}
		}
		if err != nil {
			return err
		}

		tool, err := createTool(config, context)

		if err != nil {
			return err
		}

		if _, ok := tool.(otel.Tool); !ok {
			tool = otel.NewTool(config.Type, tool)
		}

		cfg.RegisterTool(id, tool)
	}

	return nil
}

func createTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	switch strings.ToLower(cfg.Type) {

	case "scraper", "crawler":
		return scraperTool(cfg, context)

	case "search":
		return searcherTool(cfg, context)

	case "research":
		return researcherTool(cfg, context)

	case "translator":
		return translatorTool(cfg, context)

	case "mcp":
		return mcpTool(cfg, context)

	case "custom":
		return customTool(cfg, context)

	default:
		return nil, errors.New("invalid tool type: " + cfg.Type)
	}
}

func scraperTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []scrape.Option

	return scrape.New(context.Scraper, options...)
}

func searcherTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []search.Option

	return search.New(context.Searcher, options...)
}

func researcherTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []research.Option

	return research.New(context.Researcher, options...)
}

func translatorTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []translate.Option

	return translate.New(context.Translator, options...)
}

func mcpTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	exchanger, err := createClientAuth(cfg.Auth)

	if err != nil {
		return nil, err
	}

	var options []mcp.Option
	if context.Client != nil {
		options = append(options, mcp.WithClient(context.Client))
	}
	return mcp.New(cfg.URL, cfg.Vars, exchanger, options...)
}

func customTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
