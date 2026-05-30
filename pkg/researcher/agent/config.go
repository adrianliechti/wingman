package agent

import (
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/scraper"
)

type Option func(*Client)

func WithScraper(scraper scraper.Provider) Option {
	return func(c *Client) {
		c.scraper = scraper
	}
}

func WithSummarizer(p provider.Completer) Option {
	return func(c *Client) {
		c.summarizer = p
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

func WithMaxToolCalls(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxToolCalls = n
		}
	}
}

func WithMaxFetchChars(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxFetchChars = n
		}
	}
}

func WithMaxTotalFetchChars(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxTotalFetchChars = n
		}
	}
}

func WithSummarizeMinChars(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.summarizeMinChars = n
		}
	}
}
