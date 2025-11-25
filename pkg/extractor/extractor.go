package extractor

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Provider interface {
	Extract(ctx context.Context, input Input, options *ExtractOptions) (*provider.File, error)
}

var (
	ErrUnsupported = errors.New("unsupported type")
)

type Format string

const (
	FormatText  Format = "text"
	FormatImage Format = "image"
	FormatPDF   Format = "pdf"
)

type ExtractOptions struct {
	Format *Format
}

type File = provider.File

type Input struct {
	URL string

	File *provider.File
}
