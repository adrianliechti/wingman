package translator

import (
	"context"
)

type Provider interface {
	Translate(ctx context.Context, text string, options *TranslateOptions) (*Translation, error)
}

type TranslateOptions struct {
	Language string
}

type Translation struct {
	Text string
}
