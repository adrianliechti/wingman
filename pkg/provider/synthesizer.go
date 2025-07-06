package provider

import (
	"context"
)

type Synthesizer interface {
	Synthesize(ctx context.Context, input string, options *SynthesizeOptions) (*Synthesis, error)
}

type SynthesizeOptions struct {
	Voice string
	Speed *float32

	Instructions string

	Format string
}

type Synthesis struct {
	ID    string
	Model string

	Content     []byte
	ContentType string
}
