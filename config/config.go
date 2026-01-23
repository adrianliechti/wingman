package config

import (
	"bytes"
	"os"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/chain"
	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/mcp"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/segmenter"
	"github.com/adrianliechti/wingman/pkg/summarizer"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/translator"

	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Address string

	Authorizers []auth.Provider

	models map[string]provider.Model

	completer   map[string]provider.Completer
	embedder    map[string]provider.Embedder
	renderer    map[string]provider.Renderer
	reranker    map[string]provider.Reranker
	synthesizer map[string]provider.Synthesizer
	transcriber map[string]provider.Transcriber

	extractor  map[string]extractor.Provider
	segmenter  map[string]segmenter.Provider
	summarizer map[string]summarizer.Provider
	translator map[string]translator.Provider

	scraper    map[string]scraper.Provider
	searcher   map[string]searcher.Provider
	researcher map[string]researcher.Provider

	tools  map[string]tool.Provider
	chains map[string]chain.Provider

	mcps map[string]mcp.Provider
}

func Parse(path string) (*Config, error) {
	file, err := parseFile(path)

	if err != nil {
		return nil, err
	}

	c := &Config{
		Address: ":8080",
	}

	if err := c.registerAuthorizer(file); err != nil {
		return nil, err
	}

	if err := c.registerProviders(file); err != nil {
		return nil, err
	}

	if err := c.registerExtractors(file); err != nil {
		return nil, err
	}

	if err := c.registerScrapers(file); err != nil {
		return nil, err
	}

	if err := c.registerSegmenters(file); err != nil {
		return nil, err
	}

	if err := c.registerSummarizers(file); err != nil {
		return nil, err
	}

	if err := c.registerTranslators(file); err != nil {
		return nil, err
	}

	if err := c.registerSearchers(file); err != nil {
		return nil, err
	}

	if err := c.registerResearchers(file); err != nil {
		return nil, err
	}

	if err := c.registerTools(file); err != nil {
		return nil, err
	}

	if err := c.registerRouters(file); err != nil {
		return nil, err
	}

	if err := c.registerChains(file); err != nil {
		return nil, err
	}

	if err := c.registerMCP(file); err != nil {
		return nil, err
	}

	return c, nil
}

type configFile struct {
	Authorizers []authorizerConfig `yaml:"authorizers"`

	Providers []providerConfig `yaml:"providers"`

	Extractors  yaml.Node `yaml:"extractors"`
	Segmenters  yaml.Node `yaml:"segmenters"`
	Summarizers yaml.Node `yaml:"summarizers"`
	Translators yaml.Node `yaml:"translators"`

	Scrapers    yaml.Node `yaml:"scrapers"`
	Searchers   yaml.Node `yaml:"searchers"`
	Researchers yaml.Node `yaml:"researchers"`

	Tools  yaml.Node `yaml:"tools"`
	Chains yaml.Node `yaml:"chains"`

	Routers yaml.Node `yaml:"routers"`

	MCPs yaml.Node `yaml:"mcps"`
}

func parseFile(path string) (*configFile, error) {
	data, err := os.ReadFile(path)

	if err != nil {
		return nil, err
	}

	data = []byte(os.ExpandEnv(string(data)))

	var config configFile

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func createLimiter(limit *int) *rate.Limiter {
	if limit == nil {
		return nil
	}

	return rate.NewLimiter(rate.Limit(*limit), *limit)
}
