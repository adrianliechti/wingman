package audio

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"slices"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

func (h *Handler) handleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	var req SpeechRequest

	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(req.Model))
		return
	}

	synthesizer, err := h.Synthesizer(req.Model)

	if err != nil {
		writeError(w, http.StatusNotFound, shared.ModelNotFound(req.Model))
		return
	}

	if !slices.Contains([]string{"", "audio", "sse"}, req.StreamFormat) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported stream_format: "+req.StreamFormat))
		return
	}

	options := &provider.SynthesizeOptions{
		Voice: string(req.Voice),
		Speed: req.Speed,

		Format: req.ResponseFormat,

		Instructions: req.Instructions,
	}

	seq := synthesizer.Synthesize(r.Context(), req.Input, options)

	if req.StreamFormat == "sse" {
		writeSpeechStream(w, seq)
		return
	}

	writeAudioStream(w, seq)
}

// writeAudioStream forwards audio chunks as they arrive so clients start
// playing before synthesis finishes.
func writeAudioStream(w http.ResponseWriter, seq iter.Seq2[*provider.Synthesis, error]) {
	headersSent := false

	for chunk, err := range seq {
		if err != nil {
			if !headersSent {
				writeError(w, provider.CodeFromError(err, http.StatusBadGateway), err)
			}

			return
		}

		if !headersSent {
			contentType := chunk.ContentType

			if contentType == "" {
				contentType = "audio/mpeg"
			}

			w.Header().Set("Content-Type", contentType)
			headersSent = true
		}

		if len(chunk.Content) == 0 {
			continue
		}

		if _, err := w.Write(chunk.Content); err != nil {
			return
		}

		http.NewResponseController(w).Flush()
	}

	if !headersSent {
		w.Header().Set("Content-Type", "audio/mpeg")
	}
}

func writeSpeechStream(w http.ResponseWriter, seq iter.Seq2[*provider.Synthesis, error]) {
	headersSent := false

	sendHeaders := func() {
		if !headersSent {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			headersSent = true
		}
	}

	for chunk, err := range seq {
		if err != nil {
			if !headersSent {
				writeError(w, provider.CodeFromError(err, http.StatusBadGateway), err)
				return
			}

			writeErrorEvent(w, err)
			return
		}

		sendHeaders()

		if len(chunk.Content) == 0 {
			continue
		}

		writeEvent(w, SpeechDeltaEvent{
			Type: "speech.audio.delta",

			Audio: base64.StdEncoding.EncodeToString(chunk.Content),
		})
	}

	sendHeaders()

	writeEvent(w, SpeechDoneEvent{
		Type: "speech.audio.done",
	})

	fmt.Fprint(w, "data: [DONE]\n\n")

	http.NewResponseController(w).Flush()
}
