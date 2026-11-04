package exa

import (
	"net/http"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/web"

	"github.com/go-chi/chi/v5"
)

type Handler struct{ *web.Retriever }

func New(cfg *config.Config) *Handler { return &Handler{Retriever: web.New(cfg)} }

func (h *Handler) Attach(r chi.Router) {
	r.Route("/exa", func(r chi.Router) {
		r.Post("/search", h.handleSearch)
		r.Post("/contents", h.handleContents)
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			web.WriteError(w, r, http.StatusNotFound, "unsupported exa endpoint")
		})
	})
}
