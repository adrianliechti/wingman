package mcp

import (
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/config"

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
	r.HandleFunc("/mcp/{id}", h.handleMCP)
	r.HandleFunc("/mcp/{id}/*", h.handleMCP)
}

func (h *Handler) handleMCP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	handler, err := h.MCP(id)

	if err != nil {
		http.Error(w, "MCP not found", http.StatusNotFound)
		return
	}

	path := "/" + strings.Trim(r.PathValue("*"), "/")

	r.URL.Path = path
	r.RequestURI = r.URL.RequestURI()

	handler.ServeHTTP(w, r)
}
