package audio

import (
	"io"
	"mime"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
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

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	prompt := r.FormValue("prompt")
	language := r.FormValue("language")

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

	options := &provider.TranscribeOptions{
		Instructions: prompt,

		Language: language,
	}

	transcription, err := transcriber.Transcribe(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := Transcription{
		Task: "transcribe",

		// Language: transcription.Language,
		// Duration: transcription.Duration,

		Text: transcription.Text,
	}

	writeJson(w, result)
}
