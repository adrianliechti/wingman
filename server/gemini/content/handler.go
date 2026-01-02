package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server/gemini/shared"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,
	}
}

func (h *Handler) Attach(r chi.Router) {
	// POST /v1beta/models/{model}:generateContent
	r.Post("/models/{model}:generateContent", h.handleGenerateContent)

	// POST /v1beta/models/{model}:streamGenerateContent
	r.Post("/models/{model}:streamGenerateContent", h.handleStreamGenerateContent)
}

func writeJson(w http.ResponseWriter, v any) {
	shared.WriteJson(w, v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	if err != nil {
		println("server error", err.Error())
	}

	shared.WriteError(w, code, err)
}

func writeEvent(w http.ResponseWriter, v any) error {
	rc := http.NewResponseController(w)

	var data bytes.Buffer

	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	enc.Encode(v)

	event := strings.TrimSpace(data.String())

	if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
		return err
	}

	if err := rc.Flush(); err != nil {
		return err
	}

	return nil
}
