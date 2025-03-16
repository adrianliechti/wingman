package openai

import (
	"encoding/json"
	"io"
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
	}

	synthesis, err := synthesizer.Synthesize(r.Context(), req.Input, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	defer synthesis.Reader.Close()

	w.Header().Set("Content-Type", "audio/wav")
	io.Copy(w, synthesis.Reader)
}

// https://platform.openai.com/docs/api-reference/audio/createSpeech
type SpeechRequest struct {
	Model string `json:"model"`

	Input string `json:"input"`
	Voice string `json:"voice"`
}
