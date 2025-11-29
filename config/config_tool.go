package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/tool/custom"
	"github.com/adrianliechti/wingman/pkg/tool/mcp"
	"github.com/adrianliechti/wingman/pkg/tool/research"
	"github.com/adrianliechti/wingman/pkg/tool/scrape"
	"github.com/adrianliechti/wingman/pkg/tool/search"
	"github.com/adrianliechti/wingman/pkg/tool/translate"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/translator"

	"github.com/adrianliechti/wingman/pkg/otel"
)

func (c *Config) RegisterTool(id string, p tool.Provider) {
	if c.tools == nil {
		c.tools = make(map[string]tool.Provider)
	}

	c.tools[id] = p
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

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Model string `yaml:"model"`

	Extractor  string `yaml:"extractor"`
	Translator string `yaml:"translator"`

	Scraper    string `yaml:"scraper"`
	Searcher   string `yaml:"searcher"`
	Researcher string `yaml:"researcher"`
}

type toolContext struct {
	Extractor  extractor.Provider
	Translator translator.Provider

	Renderer    provider.Renderer
	Synthesizer provider.Synthesizer

	Scraper    scraper.Provider
	Searcher   searcher.Provider
	Researcher researcher.Provider
}

func (cfg *Config) registerTools(f *configFile) error {
	var configs map[string]toolConfig

	if err := f.Tools.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Tools.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := toolContext{}

		if p, err := cfg.Extractor(config.Extractor); err == nil {
			context.Extractor = p
		}

		if p, err := cfg.Translator(config.Translator); err == nil {
			context.Translator = p
		}

		if p, err := cfg.Renderer(config.Model); err == nil {
			context.Renderer = p
		}

		if p, err := cfg.Synthesizer(config.Model); err == nil {
			context.Synthesizer = p
		}

		if p, err := cfg.Scraper(config.Scraper); err == nil {
			context.Scraper = p
		}

		if p, err := cfg.Searcher(config.Searcher); err == nil {
			context.Searcher = p
		}

		if p, err := cfg.Researcher(config.Researcher); err == nil {
			context.Researcher = p
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
	return mcp.New(cfg.URL, cfg.Vars)
}

func customTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
