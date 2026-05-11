package mcp

import (
	"io"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy"

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
	r.HandleFunc("/mcp/{id}/favicon.ico", h.handleFavicon)
	r.HandleFunc("/mcp/{id}", h.handleMCP)
	r.HandleFunc("/mcp/{id}/*", h.handleMCP)
}

type faviconProvider interface {
	FaviconURL() string
}

func (h *Handler) handleFavicon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	handler, err := h.MCP(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceMCP, id, policy.ActionAccess); err != nil {
		http.NotFound(w, r)
		return
	}

	fp, ok := handler.(faviconProvider)
	if !ok {
		http.NotFound(w, r)
		return
	}

	resp, err := http.Get(fp.FaviconURL())
	if err != nil || resp.StatusCode != http.StatusOK {
		http.NotFound(w, r)
		return
	}

	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	io.Copy(w, resp.Body)
}

func (h *Handler) handleMCP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	handler, err := h.MCP(id)

	if err != nil {
		http.Error(w, "MCP not found", http.StatusNotFound)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceMCP, id, policy.ActionAccess); err != nil {
		http.Error(w, "MCP not found", http.StatusNotFound)
		return
	}

	path := "/" + strings.Trim(r.PathValue("*"), "/")

	r.URL.Path = path
	r.RequestURI = r.URL.RequestURI()

	handler.ServeHTTP(w, r)
}
