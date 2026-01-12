package gemini

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	var req CountTokensRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var totalChars int

	// Count system instruction tokens
	if req.SystemInstruction != nil {
		for _, part := range req.SystemInstruction.Parts {
			totalChars += len(part.Text)
		}
	}

	// Count content tokens
	for _, content := range req.Contents {
		totalChars += countContentChars(content)
	}

	// Estimate tokens as chars / 4
	totalTokens := totalChars / 4
	if totalTokens == 0 && totalChars > 0 {
		totalTokens = 1
	}

	writeJson(w, CountTokensResponse{
		TotalTokens: totalTokens,
	})
}

func countContentChars(content *Content) int {
	if content == nil {
		return 0
	}

	total := 0

	// Add role overhead
	total += len(content.Role)

	for _, part := range content.Parts {
		if part.Text != "" {
			total += len(part.Text)
		}

		if part.FunctionCall != nil {
			total += len(part.FunctionCall.Name)
			if args, err := json.Marshal(part.FunctionCall.Args); err == nil {
				total += len(string(args))
			}
		}

		if part.FunctionResponse != nil {
			total += len(part.FunctionResponse.Name)
			if resp, err := json.Marshal(part.FunctionResponse.Response); err == nil {
				total += len(string(resp))
			}
		}

		if part.InlineData != nil {
			// Images/media are handled differently, add fixed overhead
			total += 1000
		}
	}

	return total
}
