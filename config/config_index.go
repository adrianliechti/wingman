package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/llama/pkg/index"
	"github.com/adrianliechti/llama/pkg/index/bing"
	"github.com/adrianliechti/llama/pkg/index/chroma"
	"github.com/adrianliechti/llama/pkg/index/duckduckgo"
	"github.com/adrianliechti/llama/pkg/index/elasticsearch"
	"github.com/adrianliechti/llama/pkg/index/memory"
	"github.com/adrianliechti/llama/pkg/index/tavily"
	"github.com/adrianliechti/llama/pkg/index/weaviate"
)

type indexContext struct {
	Embedder index.Embedder
}

func (c *Config) registerIndexes(f *configFile) error {
	for id, cfg := range f.Indexes {
		var err error

		context := indexContext{}

		if cfg.Embedding != "" {
			if context.Embedder, err = c.Embedder(cfg.Embedding); err != nil {
				return err
			}
		}

		i, err := createIndex(cfg, context)

		if err != nil {
			return err
		}

		c.indexes[id] = i
	}

	return nil
}

func createIndex(cfg indexConfig, context indexContext) (index.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "chroma":
		return chromaIndex(cfg, context)

	case "memory":
		return memoryIndex(cfg, context)

	case "weaviate":
		return weaviateIndex(cfg, context)

	case "bing":
		return bingIndex(cfg)

	case "duckduckgo":
		return duckduckgoIndex(cfg)

	case "tavily":
		return tavilyIndex(cfg)

	case "elasticsearch":
		return elasticsearchIndex(cfg)

	default:
		return nil, errors.New("invalid index type: " + cfg.Type)
	}
}

func chromaIndex(cfg indexConfig, context indexContext) (index.Provider, error) {
	var options []chroma.Option

	if context.Embedder != nil {
		options = append(options, chroma.WithEmbedder(context.Embedder))
	}

	return chroma.New(cfg.URL, cfg.Namespace, options...)
}

func memoryIndex(cfg indexConfig, context indexContext) (index.Provider, error) {
	var options []memory.Option

	if context.Embedder != nil {
		options = append(options, memory.WithEmbedder(context.Embedder))
	}

	return memory.New(options...)
}

func weaviateIndex(cfg indexConfig, context indexContext) (index.Provider, error) {
	var options []weaviate.Option

	if context.Embedder != nil {
		options = append(options, weaviate.WithEmbedder(context.Embedder))
	}

	return weaviate.New(cfg.URL, cfg.Namespace, options...)
}

func bingIndex(cfg indexConfig) (index.Provider, error) {
	var options []bing.Option

	return bing.New(cfg.Token, options...)
}

func duckduckgoIndex(cfg indexConfig) (index.Provider, error) {
	var options []duckduckgo.Option

	return duckduckgo.New(options...)
}

func tavilyIndex(cfg indexConfig) (index.Provider, error) {
	var options []tavily.Option

	return tavily.New(cfg.Token, options...)
}

func elasticsearchIndex(cfg indexConfig) (index.Provider, error) {
	var options []elasticsearch.Option

	return elasticsearch.New(cfg.URL, cfg.Namespace, options...)
}
