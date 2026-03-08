package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/policy/opa"
)

type policyConfig struct {
	Type string `yaml:"type"`

	Path string `yaml:"path"`
}

func (cfg *Config) registerPolicies(f *configFile) error {
	cfg.Policy = noop.New()

	if f.Policy == nil {
		return nil
	}

	provider, err := createPolicy(*f.Policy)

	if err != nil {
		return err
	}

	cfg.Policy = provider

	return nil
}

func createPolicy(cfg policyConfig) (policy.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "opa":
		return opaPolicy(cfg)

	default:
		return nil, errors.New("invalid policy type: " + cfg.Type)
	}
}

func opaPolicy(cfg policyConfig) (policy.Provider, error) {
	return opa.New(cfg.Path)
}
