package api

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleRender(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)

	p, err := h.Renderer(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	input, err := h.readText(r)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.RenderOptions{}

	if file, err := h.readFile(r); err == nil {
		options.Images = append(options.Images, *file)
	}

	result, err := p.Render(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	contentType := result.ContentType

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Write(result.Content)
}
