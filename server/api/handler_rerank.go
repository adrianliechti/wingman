package api

import (
	"cmp"
	"encoding/json/v2"
	"net/http"
	"slices"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type RerankRequest struct {
	Model string `json:"model"`

	Query string   `json:"query"`
	Texts []string `json:"texts"`

	Limit *int `json:"limit,omitzero"`
}

type RerankResponse struct {
	Model string `json:"model"`

	Results []Result `json:"results"`
}

func (h *Handler) handleRerank(w http.ResponseWriter, r *http.Request) {
	var req RerankRequest

	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	p, err := h.Reranker(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
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
		Model:   req.Model,
		Results: []Result{},
	}

	used := make([]bool, len(req.Texts))

	for _, r := range rankings {
		index := -1
		for i, text := range req.Texts {
			if !used[i] && text == r.Text {
				index = i
				used[i] = true
				break
			}
		}

		if index < 0 {
			continue
		}

		ranking := Result{
			Index: index,
			Score: r.Score,

			Text: r.Text,
		}

		result.Results = append(result.Results, ranking)
	}

	slices.SortFunc(result.Results, func(i, j Result) int {
		return cmp.Compare(j.Score, i.Score)
	})

	writeJson(w, result)
}
