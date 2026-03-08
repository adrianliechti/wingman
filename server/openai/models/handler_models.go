package models

import (
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/policy"
)

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	result := &ModelList{
		Object: "list",
	}

	for _, m := range h.Models() {
		if h.Policy.Verify(r.Context(), policy.ResourceModel, m.ID, policy.ActionAccess) != nil {
			continue
		}

		result.Models = append(result.Models, Model{
			Object: "model",

			ID:      m.ID,
			Created: time.Now().Unix(),
			OwnedBy: "openai",
		})
	}

	writeJson(w, result)
}

func (h *Handler) handleModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, id, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	model, err := h.Model(id)

	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	result := &Model{
		Object: "model",

		ID:      model.ID,
		Created: time.Now().Unix(),
		OwnedBy: "openai",
	}

	writeJson(w, result)
}
