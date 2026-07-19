package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/tokens"
)

// handleCountTokens estimates input tokens for a Messages API request. The
// wire format is converted to the common provider format (the same conversion
// the completion path uses), and pkg/tokens picks the tokenizer family and
// framing from the model — so cross-model calls (a GPT model served through
// this Anthropic-style endpoint) are counted with the right tokenizer. No
// tokenizer data files; typical error ≤ ~10% (see pkg/tokens calibration
// tests).
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	var req CountTokensRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var system string

	if req.System != nil {
		text, err := parseSystemContent(req.System)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		system = text
	}

	messages, err := toMessages(system, req.Messages)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools, err := toTools(req.Tools)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	count := tokens.Estimate(req.Model, tokens.Input{
		Messages: messages,
		Tools:    tools,
	})

	writeJson(w, CountTokensResponse{
		InputTokens: count,
	})
}
