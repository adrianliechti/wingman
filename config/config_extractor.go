package config

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/azure"
	"github.com/adrianliechti/wingman/pkg/extractor/custom"
	"github.com/adrianliechti/wingman/pkg/extractor/docling"
	"github.com/adrianliechti/wingman/pkg/extractor/exa"
	"github.com/adrianliechti/wingman/pkg/extractor/jina"
	"github.com/adrianliechti/wingman/pkg/extractor/multi"
	"github.com/adrianliechti/wingman/pkg/extractor/tavily"
	"github.com/adrianliechti/wingman/pkg/extractor/text"
	"github.com/adrianliechti/wingman/pkg/extractor/tika"
	"github.com/adrianliechti/wingman/pkg/extractor/unstructured"
	"github.com/adrianliechti/wingman/pkg/limiter"
	"github.com/adrianliechti/wingman/pkg/otel"

	"golang.org/x/time/rate"
)

func (cfg *Config) RegisterExtractor(id string, p extractor.Provider) {
	if cfg.extractors == nil {
		cfg.extractors = make(map[string]extractor.Provider)
	}

	if _, ok := cfg.extractors[""]; !ok {
		cfg.extractors[""] = p
	}

	cfg.extractors[id] = p
}

func (cfg *Config) Extractor(id string) (extractor.Provider, error) {
	if cfg.extractors != nil {
		if c, ok := cfg.extractors[id]; ok {
			return c, nil
		}
	}

	return nil, errors.New("extractor not found: " + id)
}

type extractorConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Limit *int `yaml:"limit"`
}

type extractorContext struct {
	Limiter *rate.Limiter
}

func (cfg *Config) registerExtractors(f *configFile) error {
	var configs map[string]extractorConfig

	if err := f.Extractors.Decode(&configs); err != nil {
		return err
	}

	var extractors []extractor.Provider

	for _, node := range f.Extractors.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := extractorContext{
			Limiter: createLimiter(config.Limit),
		}

		extractor, err := createExtractor(config, context)

		if err != nil {
			return err
		}

		if _, ok := extractor.(limiter.Extractor); !ok {
			extractor = limiter.NewExtractor(context.Limiter, extractor)
		}

		if _, ok := extractor.(otel.Extractor); !ok {
			extractor = otel.NewExtractor(id, extractor)
		}

		extractors = append(extractors, extractor)

		cfg.RegisterExtractor(id, extractor)
	}

	cfg.RegisterExtractor("", multi.New(extractors...))

	return nil
}

func createExtractor(cfg extractorConfig, context extractorContext) (extractor.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "azure":
		return azureExtractor(cfg)

	case "docling":
		return doclingExtractor(cfg)

	case "exa":
		return exaExtractor(cfg)

	case "jina":
		return jinaExtractor(cfg)

	case "tavily":
		return tavilyExtractor(cfg)

	case "text":
		return textExtractor(cfg)

	case "tika":
		return tikaExtractor(cfg)

	case "unstructured":
		return unstructuredExtractor(cfg)

	case "custom", "wingman-extractor", "wingman-reader":
		return customExtractor(cfg)

	default:
		return nil, errors.New("invalid extractor type: " + cfg.Type)
	}
}

func azureExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []azure.Option

	if cfg.Token != "" {
		options = append(options, azure.WithToken(cfg.Token))
	}

	return azure.New(cfg.URL, options...)
}

func doclingExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []docling.Option

	if cfg.Token != "" {
		options = append(options, docling.WithToken(cfg.Token))
	}

	return docling.New(cfg.URL, options...)
}

func exaExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []exa.Option

	if cfg.Proxy != nil && cfg.Proxy.URL != "" {
		proxyURL, err := url.Parse(cfg.Proxy.URL)

		if err != nil {
			return nil, err
		}

		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),

				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}

		options = append(options, exa.WithClient(client))
	}

	return exa.New(cfg.Token, options...)
}

func jinaExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	return jina.New(cfg.URL, options...)
}

func textExtractor(cfg extractorConfig) (extractor.Provider, error) {
	return text.New()
}

func tavilyExtractor(cfg extractorConfig) (extractor.Provider, error) {
	return tavily.New(cfg.Token)
}

func tikaExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []tika.Option

	return tika.New(cfg.URL, options...)
}

func unstructuredExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []unstructured.Option

	if cfg.Token != "" {
		options = append(options, unstructured.WithToken(cfg.Token))
	}

	return unstructured.New(cfg.URL, options...)
}

func customExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
