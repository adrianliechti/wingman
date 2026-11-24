package audio

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

func (h *Handler) handleAudioTranscription(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	model := r.FormValue("model")

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(model))
		return
	}

	transcriber, err := h.Transcriber(model)

	if err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(model))
		return
	}

	format := r.FormValue("response_format")

	if format == "" {
		format = "json"
	}

	if !slices.Contains([]string{"json", "text", "srt", "vtt", "verbose_json", "diarized_json"}, format) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported response_format: "+format))
		return
	}

	stream := r.FormValue("stream") == "true"

	if stream && !slices.Contains([]string{"json", "text", "diarized_json"}, format) {
		writeError(w, http.StatusBadRequest, errors.New("streaming is not supported with response_format: "+format))
		return
	}

	options := &provider.TranscribeOptions{
		Instructions: r.FormValue("prompt"),

		Languages: formValues(r, "language", "languages"),
		Keywords:  formValues(r, "keywords"),

		ChunkingStrategy: r.FormValue("chunking_strategy"),
	}

	granularities := formValues(r, "timestamp_granularities")

	switch format {
	case "srt", "vtt", "verbose_json":
		options.Timestamps = true

	case "diarized_json":
		options.Diarize = true
	}

	names := formValues(r, "known_speaker_names")
	references := formValues(r, "known_speaker_references")

	for i, name := range names {
		if i >= len(references) {
			break
		}

		reference, err := decodeSpeakerReference(references[i])

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		options.Speakers = append(options.Speakers, provider.TranscribeSpeaker{
			Name: name,

			Reference: *reference,
		})
	}

	file, header, err := r.FormFile("file")

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	defer file.Close()

	data, err := io.ReadAll(file)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	contentType := header.Header.Get("Content-Type")

	if mediatype, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = mediatype
	}

	input := provider.File{
		Name: header.Filename,

		Content:     data,
		ContentType: contentType,
	}

	seq := transcriber.Transcribe(r.Context(), input, options)

	if stream {
		writeTranscriptionStream(w, seq)
		return
	}

	var acc provider.TranscriptionAccumulator

	for delta, err := range seq {
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		acc.Add(*delta)
	}

	transcription := acc.Result()

	switch format {
	case "text":
		writeText(w, transcription.Text)

	case "srt":
		writeText(w, formatSRT(segmentsOf(&transcription)))

	case "vtt":
		writeText(w, formatVTT(segmentsOf(&transcription)))

	case "verbose_json":
		writeJson(w, verboseTranscription(&transcription, slices.Contains(granularities, "word")))

	case "diarized_json":
		writeJson(w, diarizedTranscription(&transcription))

	default:
		writeJson(w, Transcription{
			Task: "transcribe",

			Language: transcription.Language,
			Duration: transcription.Duration,

			Text: transcription.Text,

			Languages: transcriptionLanguages(&transcription),
		})
	}
}

func writeText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, text)
}

func formValues(r *http.Request, names ...string) []string {
	var values []string

	for _, name := range names {
		values = append(values, r.MultipartForm.Value[name]...)
		values = append(values, r.MultipartForm.Value[name+"[]"]...)
	}

	return slices.DeleteFunc(values, func(value string) bool {
		return strings.TrimSpace(value) == ""
	})
}
