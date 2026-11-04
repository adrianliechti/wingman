package responses

import (
	"encoding/json/v2"
	"fmt"
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
	r.Post("/responses", h.handleResponses)
	r.Post("/responses/input_tokens", h.handleInputTokens)
}

func writeJson(w http.ResponseWriter, v any) {
	shared.WriteJson(w, v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	shared.WriteError(w, code, err)
}

func writeEvent(w http.ResponseWriter, eventType string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}
