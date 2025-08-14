package retriever

import (
	"context"
)

type Provider interface {
	Retrieve(ctx context.Context, query string, options *RetrieveOptions) ([]Result, error)
}

type RetrieveOptions struct {
	Limit *int

	Filters map[string]string
}

type Result struct {
	ID string

	Source string

	Score   float32
	Title   string
	Content string

	Metadata map[string]string
}
