package responses

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/tokens"
)

// InputTokensResponse mirrors the upstream POST /responses/input_tokens
// response shape.
type InputTokensResponse struct {
	Object      string `json:"object"` // response.input_tokens
	InputTokens int    `json:"input_tokens"`
}

// handleInputTokens implements POST /responses/input_tokens: estimated input
// token counts for a Responses API request. The wire format is converted to
// the common provider format (the same conversion the completion path uses),
// and pkg/tokens picks the tokenizer family and framing from the model — so
// cross-model calls (a Claude model served through this endpoint) are counted
// with the right tokenizer. No tokenizer data files; typical error ≤ ~10%
// (see pkg/tokens calibration tests).
func (h *Handler) handleInputTokens(w http.ResponseWriter, r *http.Request) {
	var req ResponsesRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.Input.Items, req.Instructions)

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

	writeJson(w, InputTokensResponse{
		Object:      "response.input_tokens",
		InputTokens: count,
	})
}
