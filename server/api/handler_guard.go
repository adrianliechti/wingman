package api

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/policy"
)

type GuardRequest struct {
	Model string `json:"model"`

	Text string `json:"text"`
}

type GuardResponse struct {
	Flagged bool `json:"flagged"`

	Categories []GuardCategory `json:"categories,omitempty"`
}

type GuardCategory struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

func (h *Handler) handleGuard(w http.ResponseWriter, r *http.Request) {
	var req GuardRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	p, err := h.Guard(req.Model)

	if err != nil {
		if req.Model == "" {
			writeJson(w, GuardResponse{})
			return
		}

		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	result, err := p.Check(r.Context(), req.Text, nil)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	response := GuardResponse{
		Flagged: result.Flagged,
	}

	for _, category := range result.Categories {
		response.Categories = append(response.Categories, GuardCategory{
			Name:  category.Name,
			Score: category.Score,
		})
	}

	writeJson(w, response)
}
