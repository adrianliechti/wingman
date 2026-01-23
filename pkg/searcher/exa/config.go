package exa

import (
	"net/http"
)

type Option func(*Client)

func WithClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
}

func WithCategory(val string) Option {
	return func(c *Client) {
		c.category = val
	}
}

func WithLocation(val string) Option {
	return func(c *Client) {
		c.location = val
	}
}
