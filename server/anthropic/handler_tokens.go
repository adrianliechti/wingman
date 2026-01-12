package anthropic

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

	// Count system tokens
	if req.System != nil {
		system, err := parseSystemContent(req.System)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		totalChars += len(system)
	}

	// Count message tokens
	for _, msg := range req.Messages {
		totalChars += countMessageChars(msg)
	}

	// Count tool tokens
	for _, tool := range req.Tools {
		totalChars += countToolChars(tool)
	}

	// Estimate tokens as chars / 4
	inputTokens := totalChars / 4
	if inputTokens == 0 && totalChars > 0 {
		inputTokens = 1
	}

	writeJson(w, CountTokensResponse{
		InputTokens: inputTokens,
	})
}

func countMessageChars(msg MessageParam) int {
	total := 0

	// Add role overhead
	total += len(string(msg.Role))

	blocks, err := parseContentBlocks(msg.Content)
	if err != nil {
		// Fallback: try to marshal and count
		if data, err := json.Marshal(msg.Content); err == nil {
			return len(string(data))
		}
		return 0
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			total += len(block.Text)
		case "tool_use":
			total += len(block.Name)
			if args, err := json.Marshal(block.Input); err == nil {
				total += len(string(args))
			}
		case "tool_result":
			if s, ok := block.Content.(string); ok {
				total += len(s)
			} else if data, err := json.Marshal(block.Content); err == nil {
				total += len(string(data))
			}
		case "image":
			// Images are handled differently by the API, add a fixed overhead
			total += 1000
		}
	}

	return total
}

func countToolChars(tool ToolParam) int {
	total := 0
	total += len(tool.Name)
	total += len(tool.Description)

	if schema, err := json.Marshal(tool.InputSchema); err == nil {
		total += len(string(schema))
	}

	return total
}
