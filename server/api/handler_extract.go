package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleExtract(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)

	p, err := h.Extractor(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	schema, err := valueSchema(r)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	input := extractor.File{
		URL: valueURL(r),
	}

	if file, header, err := r.FormFile("file"); err == nil {
		input.Name = header.Filename
		input.Reader = file
	}

	if input.URL == "" && input.Reader == nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid input"))
		return
	}

	options := &extractor.ExtractOptions{}

	document, err := p.Extract(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	contentType := document.ContentType

	if contentType != "" {
		contentType = "application/octet-stream"
	}

	if schema != nil {
		c, err := h.Completer("")

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		messages := []provider.Message{
			provider.UserMessage(document.Content),
		}

		options := &provider.CompleteOptions{
			Schema: schema,
		}

		completion, err := c.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		content := completion.Message.Content.Text()

		w.Header().Set("Content-Type", "application/json")

		w.WriteHeader(http.StatusOK)
		io.WriteString(w, content)

		return
	}

	w.Header().Set("Content-Type", contentType)

	w.WriteHeader(http.StatusOK)
	io.WriteString(w, document.Content)
}
