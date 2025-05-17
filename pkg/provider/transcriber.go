package provider

import (
	"context"
)

type Transcriber interface {
	Transcribe(ctx context.Context, input File, options *TranscribeOptions) (*Transcription, error)
}

type TranscribeOptions struct {
	Language    string
	Temperature *float32
}

type Transcription struct {
	ID    string
	Model string

	Text string

	// Language string
	// Duration float64
}
