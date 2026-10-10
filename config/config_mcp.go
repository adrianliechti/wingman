package config

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"sort"
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

func (cfg *Config) MCPs() []string {
	var result []string

	for id := range cfg.mcps {
		result = append(result, id)
	}

	sort.Strings(result)

	return result
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

	URL string `yaml:"url"`

	Tools []string `yaml:"tools"`

	Vars  map[string]string `yaml:"vars"`
	Auth  *authConfig       `yaml:"auth"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Instructions string `yaml:"instructions"`
}

type mcpContext struct {
	Tools     map[string]tool.Provider
	Transport http.RoundTripper
}

func (cfg *Config) registerMCP(f *configFile) error {
	var configs map[string]mcpConfig

	if err := decodeStrict(&f.MCPs, &configs); err != nil {
		return err
	}

	for _, id := range configIDs(&f.MCPs) {

		config, ok := configs[id]

		if !ok {
			continue
		}

		context := mcpContext{
			Tools: make(map[string]tool.Provider),
		}
		if config.Proxy != nil && strings.EqualFold(config.Type, "proxy") {
			client, err := config.Proxy.proxyClient()
			if err != nil {
				return err
			}
			context.Transport = client.Transport
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
	tools := slices.Collect(maps.Values(context.Tools))

	return server.New(cfg.Name, cfg.Instructions, tools)
}

func proxyMCP(cfg mcpConfig, context mcpContext) (mcp.Provider, error) {
	exchanger, err := createClientAuth(cfg.Auth)

	if err != nil {
		return nil, err
	}

	var options []proxy.Option
	if context.Transport != nil {
		options = append(options, proxy.WithTransport(context.Transport))
	}
	return proxy.New(cfg.URL, cfg.Vars, exchanger, options...)
}
