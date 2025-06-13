package openai

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	var req ResponseRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
}
