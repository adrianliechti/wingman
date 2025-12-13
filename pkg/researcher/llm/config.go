package llm

import (
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
)

type Option func(*Client)

func WithScraper(scraper scraper.Provider) Option {
	return func(c *Client) {
		c.scraper = scraper
	}
}

func WithEffort(effort researcher.Effort) Option {
	return func(c *Client) {
		c.effort = effort
	}
}

func WithVerbosity(verbosity researcher.Verbosity) Option {
	return func(c *Client) {
		c.verbosity = verbosity
	}
}
