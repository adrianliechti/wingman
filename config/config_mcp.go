package config

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/mcp"
	"github.com/adrianliechti/wingman/pkg/mcp/proxy"
	"github.com/adrianliechti/wingman/pkg/mcp/server"
	"github.com/adrianliechti/wingman/pkg/tool"
)

func (cfg *Config) RegisterMCP(id string, p mcp.Provider) {
	if cfg.mcps == nil {
		cfg.mcps = make(map[string]mcp.Provider)
	}

	cfg.mcps[id] = p
}

func (cfg *Config) MCP(id string) (mcp.Provider, error) {
	if cfg.mcps != nil {
		if s, ok := cfg.mcps[id]; ok {
			return s, nil
		}
	}

	return nil, errors.New("mcp not found: " + id)
}

type mcpConfig struct {
	Type string `yaml:"type"`

	Name string `yaml:"name"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Tools []string `yaml:"tools"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Instructions string `yaml:"instructions"`
}

type mcpContext struct {
	Tools map[string]tool.Provider
}

func (cfg *Config) registerMCP(f *configFile) error {
	var configs map[string]mcpConfig

	if err := f.MCPs.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.MCPs.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := mcpContext{
			Tools: make(map[string]tool.Provider),
		}

		for _, t := range config.Tools {
			tool, err := cfg.Tool(t)

			if err != nil {
				return err
			}

			context.Tools[t] = tool
		}

		mcp, err := createMCP(config, context)

		if err != nil {
			return err
		}

		cfg.RegisterMCP(id, mcp)
	}

	return nil
}

func createMCP(cfg mcpConfig, context mcpContext) (mcp.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "server":
		return serverMCP(cfg, context)
	case "proxy":
		return proxyMCP(cfg, context)
	default:
		return nil, errors.New("invalid mcp type: " + cfg.Type)
	}
}

func serverMCP(cfg mcpConfig, context mcpContext) (mcp.Provider, error) {
	name := cfg.Name

	if cfg.Name != "" {
		name = cfg.Name
	}

	tools := slices.Collect(maps.Values(context.Tools))

	return server.New(name, tools)
}

func proxyMCP(cfg mcpConfig, context mcpContext) (mcp.Provider, error) {
	return proxy.New(cfg.URL)
}
