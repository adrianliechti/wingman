package models

import (
	"net/http"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/openai/shared"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config
}

func New(cfg *config.Config) *Handler {
	h := &Handler{
		Config: cfg,
	}

	return h
}

func (h *Handler) Attach(r chi.Router) {
	r.Get("/models", h.handleModels)
	r.Get("/models/{id}", h.handleModel)
}

func writeJson(w http.ResponseWriter, v any) {
	shared.WriteJson(w, v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	shared.WriteError(w, code, err)
}
