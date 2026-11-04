package custom

import "time"

type Option func(*Client)

func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// WithTimeout bounds Check and the entire Report delivery, including retries.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) { c.timeout = timeout }
}
