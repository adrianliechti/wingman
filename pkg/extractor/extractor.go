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

type Document struct {
	Text string `json:"text"`

	Pages  []Page  `json:"pages"`
	Blocks []Block `json:"blocks"`
}

type Page struct {
	Index int `json:"index"`

	Width  int `json:"width"`
	Height int `json:"height"`
}

type Block struct {
	Page int `json:"page,omitempty"`

	Text string `json:"text,omitempty"`

	Box [4]int `json:"box,omitempty"` // [x1, y1, x2, y2]

	Confidence float64 `json:"confidence,omitempty"`
}
