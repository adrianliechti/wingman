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

	client     *http.Client
	maxRetries *int

	httpClient *http.Client
	throttle   *throttleTransport
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

func WithMaxRetries(retries int) Option {
	return func(c *Config) {
		c.maxRetries = &retries
	}
}

func (c *Config) isAzure() bool {
	return strings.Contains(c.url, "openai.azure.com") || strings.Contains(c.url, "cognitiveservices.azure.com")
}

func (c *Config) init() {
	if c.url == "" {
		c.url = "https://api.openai.com/v1/"
	}

	if c.client == nil {
		c.client = http.DefaultClient
	}

	c.url = strings.TrimRight(c.url, "/") + "/"

	if c.isAzure() {
		c.throttle = newThrottleTransport(c.client.Transport)

		c.httpClient = &http.Client{
			Transport: c.throttle,
			Timeout:   c.client.Timeout,
		}
	} else {
		c.httpClient = c.client
	}
}

// Options returns SDK request options using a plain base URL.
// For Azure this sets the Api-Key header directly and assumes the URL
// already includes the deployment path or uses the v1 flat API.
// Used by: Completer, Responder, Embedder.
func (c *Config) Options() []option.RequestOption {
	c.init()

	options := []option.RequestOption{
		option.WithBaseURL(c.url),
		option.WithHTTPClient(c.httpClient),
	}

	if c.isAzure() && c.token != "" {
		options = append(options, option.WithHeader("Api-Key", c.token))
	} else if c.token != "" {
		options = append(options, option.WithAPIKey(c.token))
	}

	if c.maxRetries != nil {
		options = append(options, option.WithMaxRetries(*c.maxRetries))
	}

	return options
}

// AzureOptions returns SDK request options using azure.WithEndpoint which
// handles deployment-based path rewriting. Required for Azure endpoints that
// are not yet available in the v1 flat API (audio, images).
// For non-Azure endpoints this falls back to Options().
// Used by: Synthesizer, Transcriber, Renderer.
func (c *Config) AzureOptions() []option.RequestOption {
	c.init()

	if !c.isAzure() {
		return c.Options()
	}

	options := []option.RequestOption{
		option.WithHTTPClient(c.httpClient),
		azure.WithEndpoint(c.url, "2025-04-01-preview"),
	}

	if c.token != "" {
		options = append(options, azure.WithAPIKey(c.token))
	}

	if c.maxRetries != nil {
		options = append(options, option.WithMaxRetries(*c.maxRetries))
	}

	return options
}

