package translator

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Provider interface {
	Translate(ctx context.Context, input Input, options *TranslateOptions) (*File, error)
}

var (
	ErrUnsupported = errors.New("unsupported type")
)

type File = provider.File

type TranslateOptions struct {
	Language string
}

type Input struct {
	Text string

	File *File
}
