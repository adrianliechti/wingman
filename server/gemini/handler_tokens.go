package gemini

import (
	"encoding/json"
	"net/http"
)

const (
	charsPerToken     = 4
	jsonCharsPerToken = 3

	// Structural framing per content (role marker + part separators).
	contentTokenOverhead = 3

	// Flat per-image estimate. Gemini bills images at 258 tokens for
	// tiles ≤384px and 258 per 768x768 tile above; 1300 approximates a
	// typical 1MP photo and matches the Anthropic estimate for parity.
	imageTokens = 1300
)

func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	var req CountTokensRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var tokens int

	if req.SystemInstruction != nil {
		tokens += contentTokens(req.SystemInstruction)
	}

	for _, content := range req.Contents {
		tokens += contentTokens(content)
	}

	for _, tool := range req.Tools {
		tokens += toolTokens(tool)
	}

	writeJson(w, CountTokensResponse{
		TotalTokens: tokens,
	})
}

func contentTokens(content *Content) int {
	if content == nil {
		return 0
	}

	total := contentTokenOverhead

	for _, part := range content.Parts {
		if part.Text != "" {
			total += textTokens(part.Text)
		}

		if part.FunctionCall != nil {
			total += textTokens(part.FunctionCall.Name)
			total += jsonTokens(part.FunctionCall.Args)
		}

		if part.FunctionResponse != nil {
			total += textTokens(part.FunctionResponse.Name)
			total += jsonTokens(part.FunctionResponse.Response)
		}

		if part.InlineData != nil {
			total += imageTokens
		}
	}

	return total
}

func toolTokens(tool *Tool) int {
	var total int

	for _, fn := range tool.FunctionDeclarations {
		total += textTokens(fn.Name)
		total += textTokens(fn.Description)

		if fn.Parameters != nil {
			total += jsonTokens(fn.Parameters)
		}

		if fn.ParametersJsonSchema != nil {
			total += jsonTokens(fn.ParametersJsonSchema)
		}
	}

	return total
}

func textTokens(s string) int {
	return len(s) / charsPerToken
}

func jsonTokens(v any) int {
	data, err := json.Marshal(v)
	if err != nil {
		return 0
	}

	return len(data) / jsonCharsPerToken
}
