package api

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleRender(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.RenderOptions{
		Aspect:     valueAspect(r),
		Quality:    provider.ParseQuality(r.FormValue("quality")),
		Resolution: provider.ParseResolution(r.FormValue("resolution")),

		Background: provider.ParseBackground(r.FormValue("background")),
		Format:     acceptFormat(r),
	}

	if files, err := readFiles(r); err == nil {
		options.Images = append(options.Images, files...)
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
