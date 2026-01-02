package ollama

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/ollama/models"

	openaichat "github.com/adrianliechti/wingman/server/openai/chat"
	openaiembeddings "github.com/adrianliechti/wingman/server/openai/embeddings"
	openaimodels "github.com/adrianliechti/wingman/server/openai/models"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config

	models *models.Handler

	openaiModels *openaimodels.Handler

	openaiChat       *openaichat.Handler
	openaiEmbeddings *openaiembeddings.Handler
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,

		models: models.New(cfg),

		openaiModels: openaimodels.New(cfg),

		openaiChat:       openaichat.New(cfg),
		openaiEmbeddings: openaiembeddings.New(cfg),
	}
}

func (h *Handler) Attach(r chi.Router) {
	r.Get("/api/version", h.handleVersion)

	r.Route("/v1", func(r chi.Router) {
		h.openaiModels.Attach(r)

		h.openaiChat.Attach(r)
		h.openaiEmbeddings.Attach(r)
	})

	h.models.Attach(r)
}

func (h *Handler) handleVersion(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"version": "0.13.5",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
