package config

import (
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/decider"
)

// Decider adapts an existing model, retaining its telemetry and retry policy.
func (cfg *Config) Decider(id string) (provider.Decider, error) {
	if p, err := cfg.Completer(id); err == nil {
		return decider.FromCompleter(id, p), nil
	}
	if p, err := cfg.Embedder(id); err == nil {
		return decider.FromEmbedder(id, p), nil
	}
	return nil, errors.New("decision model not found: " + id)
}
