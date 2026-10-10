package search

type Option func(*Client)

// WithMaxSnippetChars preserves useful provider excerpts without returning full pages.
func WithMaxSnippetChars(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxSnippetChars = n
		}
	}
}

func WithLimit(limit int) Option {
	return func(c *Client) {
		if limit > 0 {
			c.limit = min(limit, 10)
		}
	}
}
