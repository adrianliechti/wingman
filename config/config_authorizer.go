package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/header"
	"github.com/adrianliechti/wingman/pkg/auth/oidc"
	"github.com/adrianliechti/wingman/pkg/auth/static"
)

type authorizerConfig struct {
	Type string `yaml:"type"`

	Token string `yaml:"token"`

	Issuer   string `yaml:"issuer"`
	Audience string `yaml:"audience"`
}

func (c *Config) registerAuthorizer(f *configFile) error {
	for _, a := range f.Authorizers {
		authorizer, err := createAuthorizer(a)

		if err != nil {
			return err
		}

		c.Authorizers = append(c.Authorizers, authorizer)
	}

	return nil
}

func createAuthorizer(cfg authorizerConfig) (auth.Provider, error) {
	switch strings.ToLower(cfg.Type) {
	case "header":
		return headerAuthorizer(cfg)

	case "static":
		return staticAuthorizer(cfg)

	case "oidc":
		return oidcAuthorizer(cfg)

	default:
		return nil, errors.New("invalid authorizer type: " + cfg.Type)
	}
}

func headerAuthorizer(cfg authorizerConfig) (auth.Provider, error) {
	return header.New()
}

func staticAuthorizer(cfg authorizerConfig) (auth.Provider, error) {
	return static.New(cfg.Token)
}

func oidcAuthorizer(cfg authorizerConfig) (auth.Provider, error) {
	return oidc.New(cfg.Issuer, cfg.Audience)
}
