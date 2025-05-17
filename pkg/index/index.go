package index

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Embedder = provider.Embedder
type Reranker = provider.Reranker

type Provider interface {
	List(ctx context.Context, options *ListOptions) (*Page[Document], error)

	Index(ctx context.Context, documents ...Document) error
	Delete(ctx context.Context, ids ...string) error

	Query(ctx context.Context, query string, options *QueryOptions) ([]Result, error)
}

type ListOptions struct {
	Limit  *int
	Cursor string
}

type QueryOptions struct {
	Limit *int

	Filters map[string]string
}

type Page[T Document] struct {
	Items []T

	Cursor string
}

type Document struct {
	ID string

	Title   string
	Source  string
	Content string

	Metadata map[string]string

	Embedding []float32
}

type Result struct {
	Document
	Score float32
}
