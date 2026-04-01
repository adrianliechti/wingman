package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/segmenter"
	"github.com/adrianliechti/wingman/pkg/segmenter/custom"
	"github.com/adrianliechti/wingman/pkg/segmenter/jina"
	"github.com/adrianliechti/wingman/pkg/segmenter/kreuzberg"
	"github.com/adrianliechti/wingman/pkg/segmenter/text"
	"github.com/adrianliechti/wingman/pkg/segmenter/unstructured"

)

func (cfg *Config) RegisterSegmenter(id string, p segmenter.Provider) {
	if cfg.segmenter == nil {
		cfg.segmenter = make(map[string]segmenter.Provider)
	}

	if _, ok := cfg.segmenter[""]; !ok {
		cfg.segmenter[""] = p
	}

	cfg.segmenter[id] = p
}

func (cfg *Config) Segmenter(id string) (segmenter.Provider, error) {
	if cfg.segmenter != nil {
		if p, ok := cfg.segmenter[id]; ok {
			return p, nil
		}
	}

	if id == "" {
		return text.New()
	}

	return nil, errors.New("segmenter not found: " + id)
}

type segmenterConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

}

type segmenterContext struct {
	Client *http.Client
}

func (cfg *Config) registerSegmenters(f *configFile) error {
	var configs map[string]segmenterConfig

	if err := f.Segmenters.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Segmenters.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := segmenterContext{}

		if config.Proxy != nil {
			client, err := config.Proxy.proxyClient()

			if err != nil {
				return err
			}

			context.Client = client
		}

		segmenter, err := createSegmenter(config, context)

		if err != nil {
			return err
		}

		if _, ok := segmenter.(otel.Segmenter); !ok {
			segmenter = otel.NewSegmenter(id, segmenter)
		}

		cfg.RegisterSegmenter(id, segmenter)
	}

	return nil
}

func createSegmenter(cfg segmenterConfig, context segmenterContext) (segmenter.Provider, error) {
	switch strings.ToLower(cfg.Type) {

	case "jina":
		return jinaSegmenter(cfg, context)

	case "kreuzberg":
		return kreuzbergSegmenter(cfg, context)

	case "text":
		return textSegmenter(cfg)

	case "unstructured":
		return unstructuredSegmenter(cfg, context)

	case "custom", "wingman-segmenter":
		return customSegmenter(cfg)

	default:
		return nil, errors.New("invalid segmenter type: " + cfg.Type)
	}
}

func jinaSegmenter(cfg segmenterConfig, context segmenterContext) (segmenter.Provider, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	if context.Client != nil {
		options = append(options, jina.WithClient(context.Client))
	}

	return jina.New(cfg.URL, options...)
}

func kreuzbergSegmenter(cfg segmenterConfig, context segmenterContext) (segmenter.Provider, error) {
	var options []kreuzberg.Option

	if cfg.Token != "" {
		options = append(options, kreuzberg.WithToken(cfg.Token))
	}

	if context.Client != nil {
		options = append(options, kreuzberg.WithClient(context.Client))
	}

	return kreuzberg.New(cfg.URL, options...)
}

func textSegmenter(cfg segmenterConfig) (segmenter.Provider, error) {
	return text.New()
}

func unstructuredSegmenter(cfg segmenterConfig, context segmenterContext) (segmenter.Provider, error) {
	var options []unstructured.Option

	if cfg.Token != "" {
		options = append(options, unstructured.WithToken(cfg.Token))
	}

	if context.Client != nil {
		options = append(options, unstructured.WithClient(context.Client))
	}

	return unstructured.New(cfg.URL, options...)
}

func customSegmenter(cfg segmenterConfig) (*custom.Client, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
