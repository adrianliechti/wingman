package api

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type RerankRequest struct {
	Model string `json:"model"`

	Query string   `json:"query"`
	Texts []string `json:"texts"`

	Limit *int `json:"limit,omitempty"`
}

type RerankResponse struct {
	Model string `json:"model"`

	Results []Result `json:"results"`
}

func (h *Handler) handleRerank(w http.ResponseWriter, r *http.Request) {
	var req RerankRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	p, err := h.Reranker(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	rankings, err := p.Rerank(r.Context(), req.Query, req.Texts, &provider.RerankOptions{
		Limit: req.Limit,
	})

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := RerankResponse{
		Model: req.Model,
	}

	for _, r := range rankings {
		index := slices.Index(req.Texts, r.Text)

		if index < 0 {
			continue
		}

		ranking := Result{
			Index: index,
			Score: r.Score,

			Segment: Segment{
				Text: r.Text,
			},
		}

		result.Results = append(result.Results, ranking)
	}

	slices.SortFunc(result.Results, func(i, j Result) int {
		return cmp.Compare(j.Score, i.Score)
	})

	writeJson(w, result)
}
