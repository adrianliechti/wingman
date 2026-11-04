package api

import (
	"errors"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleRender(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	model := valueModel(r)

	p, err := h.Renderer(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	input := valueInput(r)

	if input == "" {
		writeError(w, http.StatusBadRequest, errors.New("input is required"))
		return
	}

	options := &provider.RenderOptions{
		Aspect:     valueAspect(r),
		Quality:    provider.ParseQuality(r.FormValue("quality")),
		Resolution: provider.ParseResolution(r.FormValue("resolution")),

		Background: provider.ParseBackground(r.FormValue("background")),
		Format:     acceptFormat(r),
	}

	options.Images, err = readFiles(r, "mask", "markup")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options.Mask, err = readNamedFile(r, "mask")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options.Markup, err = readNamedFile(r, "markup")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := p.Render(r.Context(), input, options)

	if err != nil {
		writeError(w, provider.CodeFromError(err, http.StatusBadRequest), err)
		return
	}

	contentType := result.ContentType

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Write(result.Content)
}
