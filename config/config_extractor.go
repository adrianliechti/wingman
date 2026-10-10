package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/azure"
	"github.com/adrianliechti/wingman/pkg/extractor/custom"
	"github.com/adrianliechti/wingman/pkg/extractor/docling"
	"github.com/adrianliechti/wingman/pkg/extractor/extract"
	"github.com/adrianliechti/wingman/pkg/extractor/kreuzberg"
	"github.com/adrianliechti/wingman/pkg/extractor/llm"
	"github.com/adrianliechti/wingman/pkg/extractor/mistral"
	"github.com/adrianliechti/wingman/pkg/extractor/multi"
	"github.com/adrianliechti/wingman/pkg/extractor/plain"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
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

	if id == "" {
		return defaultExtractor()
	}

	return nil, errors.New("extractor not found: " + id)
}

type extractorConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Model string `yaml:"model"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`
}

type extractorContext struct {
	Client *http.Client

	Completer provider.Completer
}

func (cfg *Config) registerExtractors(f *configFile) error {
	var configs map[string]extractorConfig

	if err := decodeStrict(&f.Extractors, &configs); err != nil {
		return err
	}

	var extractors []extractor.Provider

	for _, id := range configIDs(&f.Extractors) {

		config, ok := configs[id]

		if !ok {
			continue
		}

		context := extractorContext{}

		if strings.EqualFold(config.Type, "llm") {
			p, err := cfg.Completer(config.Model)
			if err != nil {
				return err
			}
			context.Completer = p
		}

		if config.Proxy != nil {
			client, err := config.Proxy.proxyClient()
			if err != nil {
				return err
			}
			context.Client = client
		}

		extractor, err := createExtractor(config, context)

		if err != nil {
			return err
		}

		if _, ok := extractor.(otel.Extractor); !ok {
			extractor = otel.NewExtractor(config.Type, id, extractor)
		}

		extractors = append(extractors, extractor)

		cfg.RegisterExtractor(id, extractor)
	}

	if len(extractors) > 0 {
		cfg.extractor[""] = multi.New(extractors...)
	}

	return nil
}

func createExtractor(cfg extractorConfig, context extractorContext) (extractor.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "", "default", "text":
		return defaultExtractor()

	case "llm":
		return llmExtractor(cfg, context)

	case "azure":
		return azureExtractor(cfg, context)

	case "docling":
		return doclingExtractor(cfg, context)

	case "kreuzberg":
		return kreuzbergExtractor(cfg, context)

	case "mistral":
		return mistralExtractor(cfg, context)

	case "custom", "wingman-extractor", "wingman-reader":
		return customExtractor(cfg)

	default:
		return nil, errors.New("invalid extractor type: " + cfg.Type)
	}
}

func llmExtractor(cfg extractorConfig, context extractorContext) (extractor.Provider, error) {
	if context.Completer == nil {
		return nil, errors.New("extractor model not found: " + cfg.Model)
	}

	return llm.New(context.Completer), nil
}

func azureExtractor(cfg extractorConfig, context extractorContext) (extractor.Provider, error) {
	var options []azure.Option
	if context.Client != nil {
		options = append(options, azure.WithClient(context.Client))
	}

	if cfg.Token != "" {
		options = append(options, azure.WithToken(cfg.Token))
	}

	return azure.New(cfg.URL, options...)
}

func doclingExtractor(cfg extractorConfig, context extractorContext) (extractor.Provider, error) {
	var options []docling.Option
	if context.Client != nil {
		options = append(options, docling.WithClient(context.Client))
	}

	if cfg.Token != "" {
		options = append(options, docling.WithToken(cfg.Token))
	}

	return docling.New(cfg.URL, options...)
}

func kreuzbergExtractor(cfg extractorConfig, context extractorContext) (extractor.Provider, error) {
	var options []kreuzberg.Option
	if context.Client != nil {
		options = append(options, kreuzberg.WithClient(context.Client))
	}

	if cfg.Token != "" {
		options = append(options, kreuzberg.WithToken(cfg.Token))
	}

	return kreuzberg.New(cfg.URL, options...)
}

func mistralExtractor(cfg extractorConfig, context extractorContext) (extractor.Provider, error) {
	var options []mistral.Option
	if context.Client != nil {
		options = append(options, mistral.WithClient(context.Client))
	}
	if cfg.Model != "" {
		options = append(options, mistral.WithModel(cfg.Model))
	}

	if cfg.Token != "" {
		options = append(options, mistral.WithToken(cfg.Token))
	}

	return mistral.New(options...)
}

func defaultExtractor() (extractor.Provider, error) {
	e, err := extract.New()

	if err != nil {
		return nil, err
	}

	t, err := plain.New()

	if err != nil {
		return nil, err
	}

	return multi.New(e, t), nil
}

func customExtractor(cfg extractorConfig) (extractor.Provider, error) {
	return custom.New(cfg.URL)
}
