package openai

import (
	"net/http"

	"github.com/adrianliechti/llama/pkg/provider"
)

func (h *Handler) handleAudioTranscription(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	model := r.FormValue("model")

	transcriber, err := h.Transcriber(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	prompt := r.FormValue("prompt")
	language := r.FormValue("language")

	_ = prompt
	_ = language

	file, header, err := r.FormFile("file")

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	defer file.Close()

	input := provider.File{
		Name: header.Filename,

		Content:     file,
		ContentType: header.Header.Get("Content-Type"),
	}

	options := &provider.TranscribeOptions{}

	transcription, err := transcriber.Transcribe(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := Transcription{
		Task: "transcribe",

		Language: transcription.Language,
		Duration: transcription.Duration,

		Text: transcription.Content,
	}

	writeJson(w, result)
}
