package extractor

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Provider interface {
	Extract(ctx context.Context, input File, options *ExtractOptions) (*Document, error)
}

var (
	ErrUnsupported = errors.New("unsupported type")
)

type File = provider.File

type ExtractOptions struct {
}

type Document struct {
	Text string

	Pages  []Page
	Blocks []Block
}

type Page struct {
	Page int

	Width  int
	Height int
}

type Block struct {
	Page int
	Text string

	Polygon [][2]int // [[x1, y1], [x2, y2], [x3, y3], ...]
}
