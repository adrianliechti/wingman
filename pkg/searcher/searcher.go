package searcher

import (
	"context"
	"time"
)

type Provider interface {
	Search(ctx context.Context, query string, options *SearchOptions) ([]Result, error)
	SearchCapabilities() Capabilities
}

// Capabilities describes integrated retrieval, filters and category metadata. Providers
// still validate request-specific combinations such as category restrictions.
type Capabilities struct {
	DateFilters bool
	// LocationUnsupported omits the location filter from the search tool.
	LocationUnsupported bool
	// Content reports integrated page retrieval through TextOptions.
	Content bool
	// Categories lists the canonical values exposed to tools. An empty list
	// omits category selection; topic preferences belong in the search query.
	Categories []Category
}

type Category struct {
	Name        string
	Description string
}

type SearchOptions struct {
	Limit *int

	// Text requests full page text in addition to the normal excerpts when non-nil.
	// Providers without Content require a separate scraper at the server layer.
	Text *TextOptions

	Category string
	Location string

	Include []string
	Exclude []string

	Since *time.Time // Earliest publication timestamp.
	Until *time.Time // Exclusive upper publication timestamp.
}

// TextOptions enables page text retrieval. An empty value uses provider defaults.
type TextOptions struct{}

type Result struct {
	Source string

	Title   string
	Excerpt string // Query-relevant excerpt, always part of search output.

	// Text retains complete page text retrieved with the search. Nil
	// means retrieval was not attempted; an empty value means no text was
	// available. Consumers can reuse it without fetching the source again.
	Text *string

	Timestamp *time.Time
	Score     *float64

	Metadata map[string]string
}
