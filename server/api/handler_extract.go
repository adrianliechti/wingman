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

	input := extractor.Input{}

	if url := valueURL(r); url != "" {
		input.URL = url
	}

	if file, err := h.readFile(r); err == nil {
		input.File = file
	}

	if input.URL == "" && input.File == nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if input.URL == "" && input.File == nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid input"))
		return
	}

	options := &extractor.ExtractOptions{}

	if val := valueFormat(r); val != "" {
		format := extractor.Format(val)
		options.Format = &format
	}

	result, err := p.Extract(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	contentType := result.ContentType

	if contentType != "" {
		contentType = "application/octet-stream"
	}

	if schema != nil {
		c, err := h.Completer("")

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		text := string(result.Content)

		messages := []provider.Message{
			provider.UserMessage(text),
		}

		options := &provider.CompleteOptions{
			Schema: schema,
		}

		completion, err := c.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		content := completion.Message.Text()

		w.Header().Set("Content-Type", "application/json")

		w.WriteHeader(http.StatusOK)
		io.WriteString(w, content)

		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Write(result.Content)
}
