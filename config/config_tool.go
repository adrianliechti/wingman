package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/retriever"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/tool/custom"
	"github.com/adrianliechti/wingman/pkg/tool/extract"
	"github.com/adrianliechti/wingman/pkg/tool/mcp"
	"github.com/adrianliechti/wingman/pkg/tool/render"
	"github.com/adrianliechti/wingman/pkg/tool/retrieve"
	"github.com/adrianliechti/wingman/pkg/tool/search"
	"github.com/adrianliechti/wingman/pkg/tool/synthesize"
	"github.com/adrianliechti/wingman/pkg/tool/translate"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
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

	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Model    string `yaml:"model"`
	Provider string `yaml:"provider"`

	Extractor  string `yaml:"extractor"`
	Retriever  string `yaml:"retriever"`
	Translator string `yaml:"translator"`
}

type toolContext struct {
	Extractor  extractor.Provider
	Retriever  retriever.Provider
	Translator translator.Provider

	Renderer    provider.Renderer
	Synthesizer provider.Synthesizer
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

		if p, err := cfg.Retriever(config.Retriever); err == nil {
			context.Retriever = p
		}

		if p, err := cfg.Translator(config.Translator); err == nil {
			context.Translator = p
		}

		if p, err := cfg.Extractor(config.Provider); err == nil {
			context.Extractor = p
		}

		if p, err := cfg.Translator(config.Provider); err == nil {
			context.Translator = p
		}

		if p, err := cfg.Renderer(config.Model); err == nil {
			context.Renderer = p
		}

		if p, err := cfg.Synthesizer(config.Model); err == nil {
			context.Synthesizer = p
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

	case "extractor", "crawler":
		return extractTool(cfg, context)

	case "renderer", "draw":
		return renderTool(cfg, context)

	case "retriever":
		return retrieveTool(cfg, context)

	case "search":
		return searchTool(cfg, context)

	case "synthesizer", "speak":
		return synthesizeTool(cfg, context)

	case "translator":
		return translateTool(cfg, context)

	case "mcp":
		return mcpTool(cfg, context)

	case "custom":
		return customTool(cfg, context)

	default:
		return nil, errors.New("invalid tool type: " + cfg.Type)
	}
}

func extractTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []extract.Option

	return extract.New(context.Extractor, options...)
}

func renderTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []render.Option

	return render.New(context.Renderer, options...)
}

func retrieveTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []retrieve.Option

	return retrieve.New(context.Retriever, options...)
}

func searchTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []search.Option

	return search.New(context.Retriever, options...)
}

func synthesizeTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []synthesize.Option

	return synthesize.New(context.Synthesizer, options...)
}

func translateTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []translate.Option

	return translate.New(context.Translator, options...)
}

func mcpTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	if cfg.Command != "" {
		var env []string

		for k, v := range cfg.Vars {
			env = append(env, k+"="+v)
		}

		return mcp.NewCommand(cfg.Command, env, cfg.Args)
	}

	if strings.Contains(strings.ToLower(cfg.URL), "/sse") {
		return mcp.NewSSE(cfg.URL, cfg.Vars)
	}

	return mcp.NewStreamable(cfg.URL, cfg.Vars)
}

func customTool(cfg toolConfig, context toolContext) (tool.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
