package openai

import (
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3/azure"
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
		c.url = "https://api.openai.com/v1/"
	}

	if c.client == nil {
		c.client = http.DefaultClient
	}

	c.url = strings.TrimRight(c.url, "/") + "/"

	if strings.Contains(c.url, "openai.azure.com") || strings.Contains(c.url, "cognitiveservices.azure.com") {
		options := make([]option.RequestOption, 0)

		options = append(options,
			option.WithBaseURL(c.url),
			option.WithHTTPClient(c.client),
		)

		if c.token != "" {
			options = append(options, option.WithHeader("Api-Key", c.token))
		}

		return options
	}

	options := []option.RequestOption{
		option.WithBaseURL(c.url),
		option.WithHTTPClient(c.client),
	}

	if c.token != "" {
		options = append(options, option.WithAPIKey(c.token))
	}

	return options
}

func (c *Config) HackOldAzure() []option.RequestOption {
	if c.url == "" {
		c.url = "https://api.openai.com/v1/"
	}

	if c.client == nil {
		c.client = http.DefaultClient
	}

	c.url = strings.TrimRight(c.url, "/") + "/"

	if strings.Contains(c.url, "openai.azure.com") || strings.Contains(c.url, "cognitiveservices.azure.com") {
		options := make([]option.RequestOption, 0)

		options = append(options,
			option.WithHTTPClient(c.client),
			azure.WithEndpoint(c.url, "2025-04-01-preview"),
		)

		if c.token != "" {
			options = append(options, azure.WithAPIKey(c.token))
		}

		return options
	}

	options := []option.RequestOption{
		option.WithBaseURL(c.url),
		option.WithHTTPClient(c.client),
	}

	if c.token != "" {
		options = append(options, option.WithAPIKey(c.token))
	}

	return options
}
