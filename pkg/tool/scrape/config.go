package scrape

type Option func(*Client)

func WithMaxChars(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxChars = n
		}
	}
}

func WithAllowedDomains(domains ...string) Option {
	return func(c *Client) {
		c.allowedDomains = append(c.allowedDomains, domains...)
	}
}

func WithBlockedDomains(domains ...string) Option {
	return func(c *Client) {
		c.blockedDomains = append(c.blockedDomains, domains...)
	}
}
