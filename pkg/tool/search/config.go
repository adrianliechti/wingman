package search

type Option func(*Client)

func WithLimit(limit int) Option {
	return func(c *Client) {
		if limit > 0 {
			c.limit = limit
		}
	}
}
