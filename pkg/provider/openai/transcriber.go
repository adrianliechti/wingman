package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"iter"
	"mime"
	"path"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared/constant"
)

var _ provider.Transcriber = (*Transcriber)(nil)

type Transcriber struct {
	*Config
	transcriptions openai.AudioTranscriptionService
}

func NewTranscriber(url, model string, options ...Option) (*Transcriber, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Transcriber{
		Config:         cfg,
		transcriptions: openai.NewAudioTranscriptionService(cfg.AzureOptions()...),
	}, nil
}

func (t *Transcriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) iter.Seq2[*provider.Transcription, error] {
	return func(yield func(*provider.Transcription, error) bool) {
		body, format, err := t.convertTranscribeRequest(input, options)

		if err != nil {
			yield(nil, err)
			return
		}

		id := uuid.NewString()

		if supportsTranscriptionStream(t.model, format) {
			t.stream(ctx, id, *body, yield)
			return
		}

		transcription, err := t.transcriptions.New(ctx, *body)

		if err != nil {
			yield(nil, convertError(err))
			return
		}

		yield(t.convertTranscription(id, transcription.RawJSON(), transcription.Text), nil)
	}
}

func (t *Transcriber) stream(ctx context.Context, id string, body openai.AudioTranscriptionNewParams, yield func(*provider.Transcription, error) bool) {
	stream := t.transcriptions.NewStreaming(ctx, body)

	streamedText := false

	for stream.Next() {
		event := stream.Current()

		delta := provider.Transcription{
			ID:    id,
			Model: t.model,
		}

		switch event.Type {
		case "transcript.text.delta":
			delta.Text = event.Delta
			streamedText = true

		case "transcript.text.segment":
			segment := event.AsTranscriptTextSegment()

			delta.Segments = []provider.TranscriptionSegment{
				{
					ID: segment.ID,

					Speaker: segment.Speaker,

					Start: segment.Start,
					End:   segment.End,

					Text: segment.Text,
				},
			}

		case "transcript.text.done":
			for _, l := range event.Languages {
				delta.Languages = append(delta.Languages, l.Code)
			}

			// the diarization model finalizes segments without emitting deltas
			if !streamedText {
				delta.Text = event.Text
			}

		default:
			continue
		}

		if !yield(&delta, nil) {
			return
		}
	}

	if err := stream.Err(); err != nil {
		yield(nil, convertError(err))
	}
}

func (t *Transcriber) convertTranscribeRequest(input provider.File, options *provider.TranscribeOptions) (*openai.AudioTranscriptionNewParams, openai.AudioResponseFormat, error) {
	if options == nil {
		options = new(provider.TranscribeOptions)
	}

	format := transcriptionResponseFormat(options)

	body := openai.AudioTranscriptionNewParams{
		Model: t.model,

		File: openai.File(bytes.NewReader(input.Content), transcriptionFileName(input), input.ContentType),

		ResponseFormat: format,
	}

	// only gpt-transcribe accepts a language list; other models reject it, so
	// they get the first hint as the singular language instead
	if len(options.Languages) > 1 && supportsTranscriptionContext(t.model) {
		body.Languages = options.Languages
	} else if len(options.Languages) > 0 {
		body.Language = openai.String(options.Languages[0])
	}

	if len(options.Keywords) > 0 && supportsTranscriptionContext(t.model) {
		body.Keywords = options.Keywords
	}

	if options.Instructions != "" && !isDiarizationModel(t.model) {
		body.Prompt = openai.String(options.Instructions)
	}

	if format == openai.AudioResponseFormatVerboseJSON {
		body.TimestampGranularities = []string{"segment", "word"}
	}

	for _, s := range options.Speakers {
		if s.Name == "" || len(s.Reference.Content) == 0 {
			continue
		}

		body.KnownSpeakerNames = append(body.KnownSpeakerNames, s.Name)
		body.KnownSpeakerReferences = append(body.KnownSpeakerReferences, speakerReference(s.Reference))
	}

	chunking := options.ChunkingStrategy

	// diarization rejects inputs longer than 30s without explicit chunking
	if chunking == "" && format == openai.AudioResponseFormatDiarizedJSON {
		chunking = "auto"
	}

	switch chunking {
	case "":
	default:
		body.ChunkingStrategy = openai.AudioTranscriptionNewParamsChunkingStrategyUnion{
			OfAuto: constant.ValueOf[constant.Auto](),
		}
	}

	return &body, format, nil
}

func (t *Transcriber) convertTranscription(id, raw, text string) *provider.Transcription {
	result := provider.Transcription{
		ID:    id,
		Model: t.model,

		Text: text,
	}

	var payload struct {
		Text string `json:"text"`

		Language string  `json:"language"`
		Duration float64 `json:"duration"`

		Languages []struct {
			Code string `json:"code"`
		} `json:"languages"`

		Segments []struct {
			ID jsontext.Value `json:"id"`

			Speaker string `json:"speaker"`

			Start float64 `json:"start"`
			End   float64 `json:"end"`

			Text string `json:"text"`
		} `json:"segments"`

		Words []struct {
			Word string `json:"word"`

			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words"`
	}

	if err := json.Unmarshal([]byte(raw), &payload); err == nil {
		result.Language = payload.Language
		result.Duration = payload.Duration

		for _, l := range payload.Languages {
			result.Languages = append(result.Languages, l.Code)
		}

		for i, s := range payload.Segments {
			result.Segments = append(result.Segments, provider.TranscriptionSegment{
				ID: segmentID(s.ID, i),

				Speaker: s.Speaker,

				Start: s.Start,
				End:   s.End,

				Text: s.Text,
			})
		}

		for _, w := range payload.Words {
			result.Words = append(result.Words, provider.TranscriptionWord{
				Word: w.Word,

				Start: w.Start,
				End:   w.End,
			})
		}
	}

	if result.Text == "" && len(result.Segments) > 0 {
		var texts []string

		for _, s := range result.Segments {
			texts = append(texts, strings.TrimSpace(s.Text))
		}

		result.Text = strings.Join(texts, " ")
	}

	return &result
}

// supportsTranscriptionContext reports whether the model accepts the
// gpt-transcribe context hints. `keywords` and a `languages` list are rejected
// by every other transcription model.
func supportsTranscriptionContext(model string) bool {
	model = strings.ToLower(model)

	return strings.HasPrefix(model, "gpt-transcribe") || strings.HasPrefix(model, "gpt-live-transcribe")
}

// isDiarizationModel reports whether the model rejects `prompt`.
func isDiarizationModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "diarize")
}

// supportsTranscriptionStream gates stream=true to the models that accept it —
// whisper-1 and OpenAI-compatible backends reject it.
func supportsTranscriptionStream(model string, format openai.AudioResponseFormat) bool {
	if format != openai.AudioResponseFormatJSON && format != openai.AudioResponseFormatDiarizedJSON {
		return false
	}

	model = strings.ToLower(model)

	for _, prefix := range []string{"gpt-transcribe", "gpt-live-transcribe", "gpt-4o-transcribe", "gpt-4o-mini-transcribe"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}

	return false
}

func transcriptionResponseFormat(options *provider.TranscribeOptions) openai.AudioResponseFormat {
	if options.Diarize || len(options.Speakers) > 0 {
		return openai.AudioResponseFormatDiarizedJSON
	}

	if options.Timestamps {
		return openai.AudioResponseFormatVerboseJSON
	}

	return openai.AudioResponseFormatJSON
}

func segmentID(raw jsontext.Value, index int) string {
	value := strings.TrimSpace(string(raw))

	if value == "" || value == "null" {
		return strconv.Itoa(index)
	}

	var text string

	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	return value
}

func speakerReference(input provider.File) string {
	contentType, _, _ := mime.ParseMediaType(input.ContentType)

	if !strings.HasPrefix(contentType, "audio/") {
		contentType = audioContentType(input.Name)
	}

	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(input.Content)
}

func audioContentType(name string) string {
	switch strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) {
	case "flac":
		return "audio/flac"
	case "m4a", "mp4":
		return "audio/mp4"
	case "mp3", "mpeg", "mpga":
		return "audio/mpeg"
	case "ogg":
		return "audio/ogg"
	case "webm":
		return "audio/webm"
	}

	return "audio/wav"
}

func transcriptionFileName(input provider.File) string {
	name := input.Name

	switch strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) {
	case "flac", "mp3", "mp4", "mpeg", "mpga", "m4a", "ogg", "wav", "webm":
		return name
	}

	stem := strings.TrimSuffix(name, path.Ext(name))

	switch mediaType, _, _ := mime.ParseMediaType(input.ContentType); mediaType {
	case "audio/flac", "audio/x-flac":
		return stem + ".flac"
	case "audio/mp4", "audio/x-m4a":
		return stem + ".m4a"
	case "audio/mpeg", "audio/mp3":
		return stem + ".mp3"
	case "audio/ogg":
		return stem + ".ogg"
	case "audio/wav", "audio/x-wav":
		return stem + ".wav"
	case "audio/webm":
		return stem + ".webm"
	}

	return name
}
