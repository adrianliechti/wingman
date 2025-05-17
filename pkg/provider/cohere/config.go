package cohere

import (
	"net/http"

	"github.com/cohere-ai/cohere-go/v2/option"
)

type Config struct {
	token string
	model string

	client *http.Client
}

type Option func(*Config)

func WithClient(client *http.Client) Option {
	return func(c *Config) {
		c.client = client
	}
}

func WithToken(token string) Option {
	return func(c *Config) {
		c.token = token
	}
}

func (c *Config) Options() []option.RequestOption {
	options := []option.RequestOption{}

	if c.client != nil {
		options = append(options, option.WithHTTPClient(c.client))
	}

	if c.token != "" {
		options = append(options, option.WithToken(c.token))
	}

	return options
}
