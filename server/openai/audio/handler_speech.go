package audio

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/openai/openai-go/v2"
)

func (h *Handler) handleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	var req openai.AudioSpeechNewParams

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
		Voice:  string(req.Voice),
		Format: string(req.ResponseFormat),
	}

	if req.Speed.Valid() {
		val := float32(req.Speed.Value)
		options.Speed = &val
	}

	if req.Instructions.Valid() {
		options.Instructions = req.Instructions.Value
	}

	synthesis, err := synthesizer.Synthesize(r.Context(), req.Input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	w.Header().Set("Content-Type", synthesis.ContentType)
	w.Write(synthesis.Content)
}
