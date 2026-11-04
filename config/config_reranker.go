package config

import (
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
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
	return nil, errors.New("invalid reranker type: " + cfg.Type)
}
