package extractor

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Provider interface {
	Extract(ctx context.Context, input Input, options *ExtractOptions) (*Document, error)
}

var (
	ErrUnsupported = errors.New("unsupported type")
)

type ExtractOptions struct {
}

type Input struct {
	URL *string

	File *provider.File
}

type Document struct {
	Content     []byte
	ContentType string
}
