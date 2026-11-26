package keenable

import (
	"net/http"
)

type Option func(*Client)

func WithClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
}

// WithTitle sets the app name sent as X-Keenable-Title on keyless requests.
func WithTitle(val string) Option {
	return func(c *Client) {
		if val != "" {
			c.title = val
		}
	}
}

// WithURL overrides the API origin (tests and proxies).
func WithURL(val string) Option {
	return func(c *Client) {
		if val != "" {
			c.url = val
		}
	}
}
