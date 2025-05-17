package multi

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

var _ extractor.Provider = &Extractor{}

type Extractor struct {
	providers []extractor.Provider
}

func New(provider ...extractor.Provider) *Extractor {
	return &Extractor{
		providers: provider,
	}
}

func (e *Extractor) Extract(ctx context.Context, input extractor.Input, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	for _, p := range e.providers {
		result, err := p.Extract(ctx, input, options)

		if err != nil {
			if errors.Is(err, extractor.ErrUnsupported) {
				continue
			}

			return nil, err
		}

		return result, nil
	}

	return nil, extractor.ErrUnsupported
}
