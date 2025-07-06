package openai

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	var req SpeechRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	synthesizer, err := h.Synthesizer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.SynthesizeOptions{
		Voice: req.Voice,
		Speed: req.Speed,

		Format: req.ResponseFormat,

		Instructions: req.Instructions,
	}

	synthesis, err := synthesizer.Synthesize(r.Context(), req.Input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	w.Header().Set("Content-Type", synthesis.ContentType)
	w.Write(synthesis.Content)
}
