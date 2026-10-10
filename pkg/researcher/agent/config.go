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

// WithToolCallTarget sets an advisory efficiency target, not a hard ceiling.
func WithToolCallTarget(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.toolCallTarget = n
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

// WithTotalFetchCharTarget sets an advisory target for cumulative fetched output.
func WithTotalFetchCharTarget(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.totalFetchCharTarget = n
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

// WithMaxToolCalls now sets an advisory target; all requested calls can execute.
// Deprecated: use WithToolCallTarget.
func WithMaxToolCalls(n int) Option {
	return WithToolCallTarget(n)
}

// WithMaxTotalFetchChars now sets an advisory target; individual fetches remain bounded.
// Deprecated: use WithTotalFetchCharTarget.
func WithMaxTotalFetchChars(n int) Option {
	return WithTotalFetchCharTarget(n)
}
