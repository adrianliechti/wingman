package api

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/retriever"
)

func (h *Handler) handleRetrieve(w http.ResponseWriter, r *http.Request) {
	model := valueModel(r)

	p, err := h.Retriever(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	query := valueQuery(r)

	if query == "" {
		writeError(w, http.StatusBadRequest, nil)
		return
	}

	options := &retriever.RetrieveOptions{}

	results, err := p.Retrieve(r.Context(), query, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := make([]RetrieveResult, 0)

	for _, r := range results {
		segment := RetrieveResult{
			ID: r.ID,

			Source: r.Source,

			Score:   r.Score,
			Title:   r.Title,
			Content: r.Content,

			Metadata: r.Metadata,
		}

		result = append(result, segment)
	}

	writeJson(w, result)
}

type RetrieveResult struct {
	ID string `json:"id,omitempty"`

	Source string `json:"source,omitempty"`

	Score   float32 `json:"score,omitempty"`
	Title   string  `json:"title,omitempty"`
	Content string  `json:"content,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`
}
