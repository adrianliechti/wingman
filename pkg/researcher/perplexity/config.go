package perplexity

import (
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3/option"
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

func (c *Config) Options() []option.RequestOption {
	if c.url == "" {
		c.url = "https://api.perplexity.ai/"
	}

	if c.client == nil {
		c.client = http.DefaultClient
	}

	c.url = strings.TrimRight(c.url, "/") + "/"

	options := []option.RequestOption{
		option.WithBaseURL(c.url),
		option.WithHTTPClient(c.client),
	}

	if c.token != "" {
		options = append(options, option.WithAPIKey(c.token))
	}

	return options
}
