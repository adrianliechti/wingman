package api

import (
	"io"
	"mime"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)
	language := valueLanguage(r)

	p, err := h.Transcriber(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
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

	options := &provider.TranscribeOptions{
		Language: language,
	}

	transcription, err := p.Transcribe(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain")

	w.WriteHeader(http.StatusOK)
	io.WriteString(w, transcription.Text)
}
