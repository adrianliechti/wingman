package mistral

import (
	"net/http"
)

type Option func(*Client)

func WithClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
}

func WithToken(token string) Option {
	return func(c *Client) {
		c.token = token
	}
}

var SupportedExtensions = []string{
	".pdf",
}

var SupportedMimeTypes = []string{
	"application/pdf",
}
