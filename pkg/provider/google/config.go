package google

import (
	"context"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/genai"
)

type Config struct {
	token string
	model string

	client          *http.Client
	affectiveDialog bool
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

// WithAffectiveDialog enables Gemini's native tone and emotion adaptation.
// Realtime validates model support before opening a connection.
func WithAffectiveDialog(enabled bool) Option {
	return func(c *Config) {
		c.affectiveDialog = enabled
	}
}

func (c *Config) newClient(ctx context.Context) (*genai.Client, error) {
	client := c.client

	if client == nil {
		client = provider.DefaultClient
	}

	config := &genai.ClientConfig{
		APIKey:  c.token,
		Backend: genai.BackendGeminiAPI,

		HTTPClient: client,
	}

	return genai.NewClient(ctx, config)
}
