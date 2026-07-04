package guard

import (
	"context"
	"errors"
)

type Provider interface {
	Check(ctx context.Context, text string, options *CheckOptions) (*Result, error)
}

var (
	ErrUnsupported = errors.New("unsupported type")
)

type CheckOptions struct {
}

type Result struct {
	Flagged bool

	Categories []Category
}

type Category struct {
	Name  string
	Score float64
}
