package audio

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

func transcriptionLanguages(transcription *provider.Transcription) []TranscriptionLanguage {
	var result []TranscriptionLanguage

	for _, code := range transcription.Languages {
		result = append(result, TranscriptionLanguage{
			Code: code,
		})
	}

	return result
}

// segmentsOf keeps subtitle and diarized output usable with providers that
// return plain text only, by treating the transcript as a single segment.
func segmentsOf(transcription *provider.Transcription) []provider.TranscriptionSegment {
	if len(transcription.Segments) > 0 {
		return transcription.Segments
	}

	if strings.TrimSpace(transcription.Text) == "" {
		return nil
	}

	return []provider.TranscriptionSegment{
		{
			ID: "0",

			End: transcription.Duration,

			Text: transcription.Text,
		},
	}
}

// durationOf falls back to the last segment boundary, as streamed
// transcriptions never carry an explicit duration.
func durationOf(transcription *provider.Transcription) float64 {
	if transcription.Duration > 0 {
		return transcription.Duration
	}

	if n := len(transcription.Segments); n > 0 {
		return transcription.Segments[n-1].End
	}

	return 0
}

// verboseTranscription omits words unless the caller asked for word
// granularity, matching what OpenAI returns.
func verboseTranscription(transcription *provider.Transcription, words bool) VerboseTranscription {
	result := VerboseTranscription{
		Task: "transcribe",

		Language: transcription.Language,
		Duration: durationOf(transcription),

		Text: transcription.Text,
	}

	if result.Language == "" && len(transcription.Languages) > 0 {
		result.Language = transcription.Languages[0]
	}

	for i, s := range transcription.Segments {
		result.Segments = append(result.Segments, VerboseTranscriptionSegment{
			ID: i,

			Start: s.Start,
			End:   s.End,

			Text: s.Text,
		})
	}

	if words {
		for _, w := range transcription.Words {
			result.Words = append(result.Words, TranscriptionWord{
				Word: w.Word,

				Start: w.Start,
				End:   w.End,
			})
		}
	}

	return result
}

func diarizedTranscription(transcription *provider.Transcription) DiarizedTranscription {
	result := DiarizedTranscription{
		Task: "transcribe",

		Duration: durationOf(transcription),

		Text: transcription.Text,
	}

	for _, s := range segmentsOf(transcription) {
		result.Segments = append(result.Segments, DiarizedTranscriptionSegment{
			Type: "transcript.text.segment",

			ID: s.ID,

			Speaker: s.Speaker,

			Start: s.Start,
			End:   s.End,

			Text: s.Text,
		})
	}

	return result
}

func writeTranscriptionStream(w http.ResponseWriter, seq iter.Seq2[*provider.Transcription, error]) {
	headersSent := false

	sendHeaders := func() {
		if !headersSent {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			headersSent = true
		}
	}

	var acc provider.TranscriptionAccumulator

	for delta, err := range seq {
		if err != nil {
			if !headersSent {
				writeError(w, provider.CodeFromError(err, http.StatusBadGateway), err)
				return
			}

			writeErrorEvent(w, err)
			return
		}

		sendHeaders()

		acc.Add(*delta)

		if delta.Text != "" {
			if err := writeEvent(w, TranscriptionDeltaEvent{
				Type: "transcript.text.delta",

				Delta: delta.Text,
			}); err != nil {
				writeErrorEvent(w, err)
				return
			}
		}

		for _, s := range delta.Segments {
			if err := writeEvent(w, TranscriptionSegmentEvent{
				Type: "transcript.text.segment",

				ID: s.ID,

				Speaker: s.Speaker,

				Start: s.Start,
				End:   s.End,

				Text: s.Text,
			}); err != nil {
				writeErrorEvent(w, err)
				return
			}
		}
	}

	sendHeaders()

	transcription := acc.Result()

	if err := writeEvent(w, TranscriptionDoneEvent{
		Type: "transcript.text.done",

		Text: transcription.Text,

		Languages: transcriptionLanguages(&transcription),
	}); err != nil {
		writeErrorEvent(w, err)
		return
	}

	fmt.Fprint(w, "data: [DONE]\n\n")

	http.NewResponseController(w).Flush()
}

func writeEvent(w http.ResponseWriter, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func writeErrorEvent(w http.ResponseWriter, err error) {
	shared.WriteSSERetry(w, err)

	writeEvent(w, shared.ErrorResponse{
		Error: shared.Error{
			Type:    shared.ErrorTypeFromError(err),
			Code:    provider.TypeFromError(err),
			Message: err.Error(),
		},
	})
}

func decodeSpeakerReference(value string) (*provider.File, error) {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")

	if !ok || !strings.HasPrefix(value, "data:") {
		return nil, errors.New("known_speaker_references must be data URLs")
	}

	contentType, encoding, _ := strings.Cut(meta, ";")

	if !strings.EqualFold(encoding, "base64") {
		content, err := url.PathUnescape(payload)

		if err != nil {
			return nil, err
		}

		return &provider.File{
			Content:     []byte(content),
			ContentType: contentType,
		}, nil
	}

	content, err := base64.StdEncoding.DecodeString(payload)

	if err != nil {
		if content, err = base64.RawStdEncoding.DecodeString(payload); err != nil {
			return nil, err
		}
	}

	return &provider.File{
		Content:     content,
		ContentType: contentType,
	}, nil
}

func formatSRT(segments []provider.TranscriptionSegment) string {
	var result strings.Builder

	for i, s := range segments {
		fmt.Fprintf(&result, "%d\n", i+1)
		fmt.Fprintf(&result, "%s --> %s\n", subtitleTimestamp(s.Start, ","), subtitleTimestamp(s.End, ","))
		fmt.Fprintf(&result, "%s\n\n", strings.TrimSpace(s.Text))
	}

	// OpenAI terminates srt payloads with an extra blank line
	if result.Len() > 0 {
		result.WriteString("\n")
	}

	return result.String()
}

func formatVTT(segments []provider.TranscriptionSegment) string {
	var result strings.Builder

	result.WriteString("WEBVTT\n\n")

	for _, s := range segments {
		fmt.Fprintf(&result, "%s --> %s\n", subtitleTimestamp(s.Start, "."), subtitleTimestamp(s.End, "."))
		fmt.Fprintf(&result, "%s\n\n", strings.TrimSpace(s.Text))
	}

	return result.String()
}

func subtitleTimestamp(seconds float64, decimal string) string {
	if seconds < 0 {
		seconds = 0
	}

	d := time.Duration(seconds * float64(time.Second))

	return fmt.Sprintf("%02d:%02d:%02d%s%03d",
		int(d/time.Hour),
		int(d/time.Minute%60),
		int(d/time.Second%60),
		decimal,
		int(d/time.Millisecond%1000),
	)
}
