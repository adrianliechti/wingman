package api

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/researcher"
)

func (h *Handler) handleResearch(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)

	p, err := h.Researcher(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	input := valueInput(r)

	if input == "" {
		writeError(w, http.StatusBadRequest, nil)
		return
	}

	options := &researcher.ResearchOptions{}

	result, err := p.Research(r.Context(), input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	data := ResearchResult{
		Content: result.Content,
	}

	writeJson(w, data)
}

type ResearchResult struct {
	Content string `json:"content,omitempty"`
}
