package api

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/config"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config
	http.Handler
}

func New(cfg *config.Config) (*Handler, error) {
	mux := chi.NewMux()

	h := &Handler{
		Config:  cfg,
		Handler: mux,
	}

	h.Attach(mux)
	return h, nil
}

func (h *Handler) Attach(r chi.Router) {
	r.Post("/extract", h.handleExtract)
	r.Post("/retrieve", h.handleRetrieve)

	r.Post("/rerank", h.handleRerank)
	r.Post("/segment", h.handleSegment)

	r.Post("/summarize", h.handleSummarize)
	r.Post("/translate", h.handleTranslate)
	r.Post("/transcribe", h.handleTranscribe)
}

func writeJson(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)

	text := http.StatusText(code)

	if err != nil {
		text = err.Error()
	}

	w.Write([]byte(text))
}
