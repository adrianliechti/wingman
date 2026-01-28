package anthropic

import (
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/option"
)

type Config struct {
	url string

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

func (cfg *Config) Options() []option.RequestOption {
	url := cfg.url

	if url == "" {
		url = "https://api.anthropic.com/"
	}

	url = strings.TrimRight(url, "/") + "/"

	options := []option.RequestOption{
		option.WithBaseURL(url),
	}

	if cfg.client != nil {
		options = append(options, option.WithHTTPClient(cfg.client))
	}

	if cfg.token != "" {
		options = append(options, option.WithAPIKey(cfg.token))
	}

	return options
}
