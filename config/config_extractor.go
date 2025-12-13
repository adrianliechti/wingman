package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/azure"
	"github.com/adrianliechti/wingman/pkg/extractor/custom"
	"github.com/adrianliechti/wingman/pkg/extractor/docling"
	"github.com/adrianliechti/wingman/pkg/extractor/kreuzberg"
	"github.com/adrianliechti/wingman/pkg/extractor/multi"
	"github.com/adrianliechti/wingman/pkg/extractor/text"
	"github.com/adrianliechti/wingman/pkg/extractor/tika"
	"github.com/adrianliechti/wingman/pkg/extractor/unstructured"
	"github.com/adrianliechti/wingman/pkg/limiter"
	"github.com/adrianliechti/wingman/pkg/otel"

	"golang.org/x/time/rate"
)

func (cfg *Config) RegisterExtractor(id string, p extractor.Provider) {
	if cfg.extractor == nil {
		cfg.extractor = make(map[string]extractor.Provider)
	}

	if _, ok := cfg.extractor[""]; !ok {
		cfg.extractor[""] = p
	}

	cfg.extractor[id] = p
}

func (cfg *Config) Extractor(id string) (extractor.Provider, error) {
	if cfg.extractor != nil {
		if c, ok := cfg.extractor[id]; ok {
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
			extractor = otel.NewExtractor(id, "", extractor)
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

	case "kreuzberg":
		return kreuzbergExtractor(cfg)

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

func kreuzbergExtractor(cfg extractorConfig) (extractor.Provider, error) {
	var options []kreuzberg.Option

	if cfg.Token != "" {
		options = append(options, kreuzberg.WithToken(cfg.Token))
	}

	return kreuzberg.New(cfg.URL, options...)
}

func textExtractor(cfg extractorConfig) (extractor.Provider, error) {
	return text.New()
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
