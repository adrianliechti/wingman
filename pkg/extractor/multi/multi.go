package multi

import (
	"context"
	"errors"
	"slices"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

var _ extractor.Provider = &Extractor{}

type Extractor struct {
	providers []extractor.Provider
}

func New(provider ...extractor.Provider) *Extractor {
	return &Extractor{
		providers: slices.Clone(provider),
	}
}

func (e *Extractor) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	var failures error
	for _, p := range e.providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p == nil || !p.Capabilities().MaySupport(file) {
			continue
		}
		result, err := p.Extract(ctx, file, options)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			if !errors.Is(err, extractor.ErrUnsupported) {
				failures = errors.Join(failures, err)
			}
			continue
		}
		if result == nil {
			failures = errors.Join(failures, errors.New("extractor: empty response"))
			continue
		}

		return result, nil
	}

	if failures != nil {
		return nil, failures
	}
	return nil, extractor.ErrUnsupported
}

func (e *Extractor) Capabilities() extractor.Capabilities {
	var result extractor.Capabilities
	for _, p := range e.providers {
		if p == nil {
			continue
		}
		capability := p.Capabilities()
		result.MediaTypes = append(result.MediaTypes, capability.MediaTypes...)
		result.Extensions = append(result.Extensions, capability.Extensions...)
		result.UnknownFormats = result.UnknownFormats || capability.UnknownFormats
	}
	slices.Sort(result.MediaTypes)
	result.MediaTypes = slices.Compact(result.MediaTypes)
	slices.Sort(result.Extensions)
	result.Extensions = slices.Compact(result.Extensions)
	return result
}
