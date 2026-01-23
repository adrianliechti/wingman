package searcher

import (
	"context"
)

type Provider interface {
	Search(ctx context.Context, query string, options *SearchOptions) ([]Result, error)
}

type SearchOptions struct {
	Limit *int

	Category string
	Location string

	Include []string
	Exclude []string
}

type Result struct {
	Source string

	Title   string
	Content string

	Metadata map[string]string
}
