package gemini

import (
	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/gemini/content"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config

	content *content.Handler
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,

		content: content.New(cfg),
	}
}

func (h *Handler) Attach(r chi.Router) {
	h.content.Attach(r)
}
