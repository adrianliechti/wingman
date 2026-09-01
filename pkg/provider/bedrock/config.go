package bedrock

import (
	"net/http"
	"strings"
)

type Config struct {
	model string

	client *http.Client
	region string
	voice  string
}

type Option func(*Config)

func WithClient(client *http.Client) Option {
	return func(c *Config) {
		c.client = client
	}
}

func WithRegion(region string) Option {
	return func(c *Config) {
		c.region = region
	}
}

func WithVoice(voice string) Option {
	return func(c *Config) {
		c.voice = voice
	}
}

func isClaudeModel(model string) bool {
	model = strings.ToLower(model)

	return strings.Contains(model, "anthropic") || strings.Contains(model, "claude")
}
