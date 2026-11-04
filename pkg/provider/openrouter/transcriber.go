package openrouter

import (
	"context"
	"encoding/base64"
	"iter"
	"path"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Transcriber = (*Transcriber)(nil)

type Transcriber struct {
	*Config
}

func NewTranscriber(model string, options ...Option) (*Transcriber, error) {
	return &Transcriber{
		Config: newConfig(model, options...),
	}, nil
}

func (t *Transcriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) iter.Seq2[*provider.Transcription, error] {
	return func(yield func(*provider.Transcription, error) bool) {
		transcription, err := t.transcribe(ctx, input, options)

		if err != nil {
			yield(nil, err)
			return
		}

		yield(transcription, nil)
	}
}

func (t *Transcriber) transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) (*provider.Transcription, error) {
	if options == nil {
		options = new(provider.TranscribeOptions)
	}

	body := map[string]any{
		"model": t.model,

		"input_audio": map[string]any{
			"data":   base64.StdEncoding.EncodeToString(input.Content),
			"format": detectAudioFormat(input),
		},
	}

	if len(options.Languages) > 0 {
		body["language"] = options.Languages[0]
	}

	if options.Instructions != "" {
		body["prompt"] = options.Instructions
	}

	var result struct {
		Text string `json:"text"`
	}

	if err := doRequest(ctx, t.client, t.url+"/audio/transcriptions", t.token, body, &result); err != nil {
		return nil, err
	}

	return &provider.Transcription{
		ID:    uuid.NewString(),
		Model: t.model,

		Text: strings.TrimSpace(result.Text),
	}, nil
}

func detectAudioFormat(file provider.File) string {
	if file.ContentType != "" {
		// Extract subtype from MIME type (e.g. "audio/mp3" -> "mp3")
		if maintype, subtype, ok := strings.Cut(file.ContentType, "/"); ok && maintype == "audio" {
			switch subtype {
			case "x-wav":
				return "wav"
			case "mpeg":
				return "mp3"
			case "x-aiff":
				return "aiff"
			case "mp4", "x-m4a":
				return "m4a"
			default:
				return subtype
			}
		}
	}

	if file.Name != "" {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(file.Name), "."))

		switch ext {
		case "aif":
			return "aiff"
		case "weba":
			return "webm"
		default:
			if ext != "" {
				return ext
			}
		}
	}

	return "wav"
}
