package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/summarizer"
	"github.com/adrianliechti/wingman/pkg/summarizer/custom"

	"golang.org/x/time/rate"
)

func (cfg *Config) RegisterSummarizer(id string, p summarizer.Provider) {
	if cfg.summarizer == nil {
		cfg.summarizer = make(map[string]summarizer.Provider)
	}

	if _, ok := cfg.summarizer[""]; !ok {
		cfg.summarizer[""] = p
	}

	cfg.summarizer[id] = p
}

func (cfg *Config) Summarizer(id string) (summarizer.Provider, error) {
	if cfg.summarizer != nil {
		if p, ok := cfg.summarizer[id]; ok {
			return p, nil
		}
	}

	return nil, errors.New("summarizer not found: " + id)
}

type summarizerConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Limit *int `yaml:"limit"`
}

type summarizerContext struct {
	Limiter *rate.Limiter
}

func (cfg *Config) registerSummarizers(f *configFile) error {
	var configs map[string]summarizerConfig

	if err := f.Summarizers.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Summarizers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := summarizerContext{
			Limiter: createLimiter(config.Limit),
		}

		summarizer, err := createSummarizer(config, context)

		if err != nil {
			return err
		}

		cfg.RegisterSummarizer(id, summarizer)
	}

	return nil
}

func createSummarizer(cfg summarizerConfig, context summarizerContext) (summarizer.Provider, error) {
	switch strings.ToLower(cfg.Type) {

	case "custom":
		return customSummarizer(cfg, context)

	default:
		return nil, errors.New("invalid translator type: " + cfg.Type)
	}
}

func customSummarizer(cfg summarizerConfig, context summarizerContext) (summarizer.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
