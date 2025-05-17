package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/huggingface"
	"github.com/adrianliechti/wingman/pkg/provider/jina"
)

func (cfg *Config) RegisterReranker(id string, p provider.Reranker) {
	cfg.RegisterModel(id)

	if cfg.reranker == nil {
		cfg.reranker = make(map[string]provider.Reranker)
	}

	if _, ok := cfg.reranker[""]; !ok {
		cfg.reranker[""] = p
	}

	cfg.reranker[id] = p
}

func (cfg *Config) Reranker(id string) (provider.Reranker, error) {
	if cfg.reranker != nil {
		if e, ok := cfg.reranker[id]; ok {
			return e, nil
		}
	}

	return nil, errors.New("reranker not found: " + id)
}

func createReranker(cfg providerConfig, model modelContext) (provider.Reranker, error) {
	switch strings.ToLower(cfg.Type) {
	case "huggingface":
		return huggingfaceReranker(cfg, model)

	case "jina", "wingman-reranker":
		return jinaReranker(cfg, model)

	default:
		return nil, errors.New("invalid reranker type: " + cfg.Type)
	}
}

func huggingfaceReranker(cfg providerConfig, model modelContext) (provider.Reranker, error) {
	var options []huggingface.Option

	if cfg.Token != "" {
		options = append(options, huggingface.WithToken(cfg.Token))
	}

	return huggingface.NewReranker(cfg.URL, model.ID, options...)
}

func jinaReranker(cfg providerConfig, model modelContext) (provider.Reranker, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	return jina.NewReranker(cfg.URL, model.ID, options...)
}
