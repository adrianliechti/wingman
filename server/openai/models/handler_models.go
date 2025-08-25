package models

import (
	"net/http"
	"time"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/packages/pagination"
)

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	result := pagination.Page[openai.Model]{
		Object: "list",
	}

	for _, m := range h.Models() {
		result.Data = append(result.Data, openai.Model{
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

	result := openai.Model{
		ID:      model.ID,
		Created: time.Now().Unix(),
		OwnedBy: "openai",
	}

	writeJson(w, result)
}
