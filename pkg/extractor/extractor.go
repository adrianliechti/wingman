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

	Unit   string
	Width  float64
	Height float64
}

type BlockState string

const (
	BlockTypeNone       BlockState = ""
	BlockStateChecked   BlockState = "checked"
	BlockStateUnchecked BlockState = "unchecked"
)

type Block struct {
	Page int

	Text  string
	State BlockState

	Score   float64
	Polygon [][2]float64 // [[x1, y1], [x2, y2], [x3, y3], ...]
}
