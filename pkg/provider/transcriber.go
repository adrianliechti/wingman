package provider

import (
	"context"
)

type Transcriber interface {
	Transcribe(ctx context.Context, input File, options *TranscribeOptions) (*Transcription, error)
}

type TranscribeOptions struct {
	Language     string
	Instructions string
}

type Transcription struct {
	ID    string
	Model string

	Text string

	// Language string
	// Duration float64
}
