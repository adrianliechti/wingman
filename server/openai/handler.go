package openai

import (
	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/openai/audio"
	"github.com/adrianliechti/wingman/server/openai/chat"
	"github.com/adrianliechti/wingman/server/openai/embeddings"
	"github.com/adrianliechti/wingman/server/openai/image"
	"github.com/adrianliechti/wingman/server/openai/models"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config

	models *models.Handler

	chat  *chat.Handler
	audio *audio.Handler
	image *image.Handler

	embeddings *embeddings.Handler
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,

		models: models.New(cfg),

		chat:  chat.New(cfg),
		audio: audio.New(cfg),
		image: image.New(cfg),

		embeddings: embeddings.New(cfg),
	}
}

func (h *Handler) Attach(r chi.Router) {
	h.models.Attach(r)

	h.chat.Attach(r)
	h.audio.Attach(r)
	h.image.Attach(r)

	h.embeddings.Attach(r)
}
