package models

import (
	"net/http"
	"time"
)

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	result := &ModelList{
		Object: "list",
	}

	for _, m := range h.Models() {
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
	model, err := h.Model(r.PathValue("id"))

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
