package google

import (
	"bytes"
	"context"
	"iter"
	"mime"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
	"google.golang.org/genai"
)

var _ provider.Transcriber = (*Transcriber)(nil)

type Transcriber struct {
	*Config
}

func NewTranscriber(model string, options ...Option) (*Transcriber, error) {
	cfg := &Config{
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Transcriber{
		Config: cfg,
	}, nil
}

// Transcribe runs a Gemini transcribe model (gemini-3.5-transcribe) through
// streamGenerateContent with an audioTranscriptionConfig. Every utterance the
// model returns arrives as a part carrying an audioTranscription with the
// text, an optional speaker label and optional word timings. Diarization
// always requests word timestamps so segments carry real boundaries. Custom
// vocabulary is rejected next to diarization or timestamps, so keywords are
// dropped in that case rather than failing the request. Speaker references
// have no counterpart; known speaker names are applied in order of first
// appearance.
func (t *Transcriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) iter.Seq2[*provider.Transcription, error] {
	return func(yield func(*provider.Transcription, error) bool) {
		if options == nil {
			options = new(provider.TranscribeOptions)
		}

		client, err := t.newClient(ctx)

		if err != nil {
			yield(nil, err)
			return
		}

		parts := []*genai.Part{
			{
				InlineData: &genai.Blob{
					MIMEType: audioMIMEType(input),
					Data:     input.Content,
				},
			},
		}

		if options.Instructions != "" {
			parts = append(parts, genai.NewPartFromText(options.Instructions))
		}

		contents := []*genai.Content{
			genai.NewContentFromParts(parts, genai.RoleUser),
		}

		diarize := options.Diarize || len(options.Speakers) > 0
		timestamps := options.Timestamps || diarize

		config := &genai.AudioTranscriptionConfig{
			LanguageCodes: options.Languages,
		}

		if diarize {
			config.Diarization = new(true)
		}

		if timestamps {
			config.WordTimestamp = new(true)
		}

		if !timestamps {
			config.CustomVocabulary = options.Keywords
		}

		generateConfig := &genai.GenerateContentConfig{
			AudioTranscriptionConfig: config,
		}

		id := uuid.NewString()

		state := &transcriptState{
			structured: timestamps,
			labels:     map[string]int{},
		}

		for _, speaker := range options.Speakers {
			state.names = append(state.names, speaker.Name)
		}

		for resp, err := range client.Models.GenerateContentStream(ctx, t.model, contents, generateConfig) {
			if err != nil {
				yield(nil, convertError(err))
				return
			}

			if resp.ResponseID != "" {
				id = resp.ResponseID
			}

			delta := state.convert(resp)

			if delta == nil {
				continue
			}

			delta.ID = id
			delta.Model = t.model

			if !yield(delta, nil) {
				return
			}
		}

		duration := state.end

		if duration == 0 {
			duration = wavDuration(input.Content)
		}

		// A trailing delta carries the duration, and gives silent audio an
		// empty transcript instead of an error.
		yield(&provider.Transcription{
			ID:    id,
			Model: t.model,

			Duration: duration,
		}, nil)
	}
}

// transcriptState turns streamed utterances into deltas: it numbers segments,
// maps speaker labels to names, separates utterances with a space and tracks
// the last known end offset.
type transcriptState struct {
	structured bool

	names  []string
	labels map[string]int

	segments int
	emitted  bool

	end float64
}

func (s *transcriptState) convert(resp *genai.GenerateContentResponse) *provider.Transcription {
	var result provider.Transcription

	var text strings.Builder

	for _, candidate := range resp.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}

		for _, part := range candidate.Content.Parts {
			if part == nil {
				continue
			}

			utterance := part.AudioTranscription

			if utterance == nil {
				// Plain text without metadata; a part with a transcription
				// repeats its text in Text, so only use it on its own.
				if strings.TrimSpace(part.Text) == "" {
					continue
				}

				utterance = &genai.Transcription{Text: part.Text}
			}

			content := strings.TrimSpace(utterance.Text)

			var words []provider.TranscriptionWord

			for _, w := range utterance.Words {
				if w == nil {
					continue
				}

				words = append(words, provider.TranscriptionWord{
					Word: w.Word,

					Start: parseOffset(w.StartOffset),
					End:   parseOffset(w.EndOffset),
				})
			}

			if content == "" && len(words) == 0 {
				continue
			}

			if content != "" {
				if s.emitted {
					text.WriteString(" ")
				}

				text.WriteString(content)
				s.emitted = true
			}

			if result.Language == "" {
				result.Language = utterance.LanguageCode
			}

			result.Words = append(result.Words, words...)

			if s.structured {
				segment := provider.TranscriptionSegment{
					ID: strconv.Itoa(s.segments),

					Speaker: s.speaker(utterance.SpeakerLabel),

					Start: s.end,
					End:   s.end,

					Text: content,
				}

				if utterance.StartOffset > 0 || utterance.EndOffset > 0 {
					segment.Start = utterance.StartOffset.Seconds()
					segment.End = utterance.EndOffset.Seconds()
				}

				if len(words) > 0 {
					segment.Start = words[0].Start
					segment.End = words[len(words)-1].End
				}

				result.Segments = append(result.Segments, segment)

				s.segments++
			}

			if utterance.EndOffset > 0 {
				s.end = max(s.end, utterance.EndOffset.Seconds())
			}

			if len(words) > 0 {
				s.end = max(s.end, words[len(words)-1].End)
			}
		}
	}

	if text.Len() == 0 && len(result.Segments) == 0 && len(result.Words) == 0 {
		return nil
	}

	result.Text = text.String()

	return &result
}

// speaker maps a Gemini label such as spk:0 to a known speaker name, or to a
// letter in order of first appearance like OpenAI's diarized output.
func (s *transcriptState) speaker(label string) string {
	if label == "" {
		return ""
	}

	index, ok := s.labels[label]

	if !ok {
		index = len(s.labels)
		s.labels[label] = index
	}

	if index < len(s.names) {
		return s.names[index]
	}

	if index < 26 {
		return string(rune('A' + index))
	}

	return label
}

// parseOffset reads a protobuf duration such as 0.100s or 2s.
func parseOffset(value string) float64 {
	if value == "" {
		return 0
	}

	duration, err := time.ParseDuration(value)

	if err != nil {
		return 0
	}

	return duration.Seconds()
}

// wavDuration returns the playing time of a RIFF/WAVE payload, or 0 for any
// other format.
func wavDuration(data []byte) float64 {
	samples, format, ok := parseWAV(data)

	if !ok || len(samples) == 0 {
		return 0
	}

	bytesPerSecond := format.sampleRate * format.channels * format.bitsPerSample / 8

	if bytesPerSecond <= 0 {
		return 0
	}

	return float64(len(samples)) / float64(bytesPerSecond)
}

// audioMIMEType picks a Gemini-supported audio MIME type from the upload's
// content type, its file extension, or a magic-number sniff.
func audioMIMEType(file provider.File) string {
	if mediatype, _, err := mime.ParseMediaType(file.ContentType); err == nil && strings.HasPrefix(mediatype, "audio/") {
		switch mediatype {
		case "audio/x-wav", "audio/wave", "audio/vnd.wave":
			return "audio/wav"
		case "audio/mp4", "audio/x-m4a":
			return "audio/m4a"
		case "audio/x-aiff":
			return "audio/aiff"
		case "audio/x-flac":
			return "audio/flac"
		case "audio/pcm", "audio/L16":
			return "audio/l16"
		}

		return mediatype
	}

	switch strings.ToLower(path.Ext(file.Name)) {
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mp3"
	case ".m4a", ".mp4":
		return "audio/m4a"
	case ".aac":
		return "audio/aac"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	case ".flac":
		return "audio/flac"
	case ".aiff", ".aif":
		return "audio/aiff"
	case ".webm", ".weba":
		return "audio/webm"
	case ".pcm":
		return "audio/l16"
	}

	content := file.Content

	switch {
	case bytes.HasPrefix(content, []byte("RIFF")):
		return "audio/wav"
	case bytes.HasPrefix(content, []byte("fLaC")):
		return "audio/flac"
	case bytes.HasPrefix(content, []byte("OggS")):
		return "audio/ogg"
	case bytes.HasPrefix(content, []byte("FORM")):
		return "audio/aiff"
	case bytes.HasPrefix(content, []byte("ID3")):
		return "audio/mp3"
	case len(content) >= 2 && content[0] == 0xFF && content[1]&0xE0 == 0xE0:
		return "audio/mp3"
	}

	return "audio/mp3"
}
