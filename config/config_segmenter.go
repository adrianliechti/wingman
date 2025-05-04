package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/limiter"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/segmenter"
	"github.com/adrianliechti/wingman/pkg/segmenter/jina"
	"github.com/adrianliechti/wingman/pkg/segmenter/text"
	"github.com/adrianliechti/wingman/pkg/segmenter/unstructured"

	"golang.org/x/time/rate"
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

	Limit *int `yaml:"limit"`
}

type segmenterContext struct {
	Limiter *rate.Limiter
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

		context := segmenterContext{
			Limiter: createLimiter(config.Limit),
		}

		segmenter, err := createSegmenter(config, context)

		if err != nil {
			return err
		}

		if _, ok := segmenter.(limiter.Segmenter); !ok {
			segmenter = limiter.NewSegmenter(context.Limiter, segmenter)
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

	case "jina", "wingman":
		return jinaSegmenter(cfg)

	case "text":
		return textSegmenter(cfg)

	case "unstructured":
		return unstructuredSegmenter(cfg)

	default:
		return nil, errors.New("invalid segmenter type: " + cfg.Type)
	}
}

func jinaSegmenter(cfg segmenterConfig) (segmenter.Provider, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	return jina.New(cfg.URL, options...)
}

func textSegmenter(cfg segmenterConfig) (segmenter.Provider, error) {
	return text.New()
}

func unstructuredSegmenter(cfg segmenterConfig) (segmenter.Provider, error) {
	var options []unstructured.Option

	if cfg.Token != "" {
		options = append(options, unstructured.WithToken(cfg.Token))
	}

	return unstructured.New(cfg.URL, options...)
}
