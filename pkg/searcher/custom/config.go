package custom

type Option func(*Client)

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
