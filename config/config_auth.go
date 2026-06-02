package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth/obo"
)

type authConfig struct {
	Type string `yaml:"type"`

	// obo
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	Scope        string `yaml:"scope"`
}

// createClientAuth builds the outbound auth (token exchanger) for an MCP or tool
// connection. It returns nil when no auth block is configured.
func createClientAuth(cfg *authConfig) (*obo.Exchanger, error) {
	if cfg == nil {
		return nil, nil
	}

	switch strings.ToLower(cfg.Type) {
	case "obo":
		return obo.New(cfg.Issuer, cfg.ClientID, cfg.ClientSecret, cfg.Scope)

	default:
		return nil, errors.New("invalid auth type: " + cfg.Type)
	}
}
