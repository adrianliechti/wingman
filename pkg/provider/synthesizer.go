package provider

import (
	"context"
)

type Synthesizer interface {
	Synthesize(ctx context.Context, input string, options *SynthesizeOptions) (*Synthesis, error)
}

type SynthesizeOptions struct {
	Voice string
}

type Synthesis struct {
	ID string

	Content     []byte
	ContentType string
}
