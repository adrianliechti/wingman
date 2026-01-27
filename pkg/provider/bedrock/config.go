package bedrock

import (
	"net/http"
	"strings"
)

type Config struct {
	model string

	client *http.Client
}

type Option func(*Config)

func WithClient(client *http.Client) Option {
	return func(c *Config) {
		c.client = client
	}
}

func isClaudeModel(model string) bool {
	model = strings.ToLower(model)

	return strings.Contains(model, "anthropic") || strings.Contains(model, "claude")
}
