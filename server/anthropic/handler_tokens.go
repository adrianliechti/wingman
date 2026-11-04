package anthropic

import (
	"fmt"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/policy"
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
	if err := decodeRequest(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateAPIVersion(r.Header.Get("anthropic-version"), req.Messages); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Model == "" || len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("model and non-empty messages are required"))
		return
	}

	if _, err := h.Completer(req.Model); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
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
