package searcher

import (
	"context"
	"time"
)

type Provider interface {
	Search(ctx context.Context, query string, options *SearchOptions) ([]Result, error)
	Capabilities() Capabilities
}

// Capabilities describes available filters and category metadata. Providers
// still validate request-specific combinations such as category restrictions.
type Capabilities struct {
	DateFilters bool
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

	Category string
	Location string

	Include []string
	Exclude []string

	Since *time.Time // Earliest publication timestamp.
	Until *time.Time // Exclusive upper publication timestamp.
}

type Result struct {
	Source string

	Title   string
	Content string

	Timestamp *time.Time

	Metadata map[string]string
}
