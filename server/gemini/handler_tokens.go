package gemini

import (
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tokens"
)

// handleCountTokens estimates the prompt tokens of a request. The official
// body is either bare `contents` or a full `generateContentRequest`; the
// latter carries the system instruction and tools. The count comes from
// pkg/tokens, which picks the tokenizer family from the route model, so a
// GPT or Claude model served through this endpoint is counted with its own
// tokenizer. Estimates, not billing-accurate counts.
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")

	var req CountTokensRequest

	if err := decodeRequest(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if _, err := h.Completer(model); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	contents := req.Contents
	system := req.SystemInstruction
	toolDefs := req.Tools

	if nested := req.GenerateContentRequest; nested != nil {
		contents = nested.Contents
		system = nested.SystemInstruction
		toolDefs = nested.Tools
	}

	messages, err := toMessages(system, contents)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools := toTools(toolDefs, false)

	total := tokens.Estimate(model, tokens.Input{Messages: messages, Tools: tools})

	writeJson(w, CountTokensResponse{
		TotalTokens:         total,
		PromptTokensDetails: modalityDetails(model, messages, total),
	})
}

// modalityDetails splits the estimate by modality the way Gemini reports it:
// media parts are estimated on their own, the remainder is text.
func modalityDetails(model string, messages []provider.Message, total int) []*ModalityTokenCount {
	byModality := map[string]int{}
	var order []string

	empty := tokens.Estimate(model, tokens.Input{Messages: []provider.Message{{Role: provider.MessageRoleUser}}})

	for _, m := range messages {
		for _, c := range m.Content {
			if c.File == nil {
				continue
			}

			modality := fileModality(c.File.ContentType)

			single := tokens.Estimate(model, tokens.Input{Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: []provider.Content{c}}}})

			if _, seen := byModality[modality]; !seen {
				order = append(order, modality)
			}

			byModality[modality] += max(single-empty, 0)
		}
	}

	media := 0
	for _, count := range byModality {
		media += count
	}

	details := []*ModalityTokenCount{{Modality: "TEXT", TokenCount: max(total-media, 0)}}

	for _, modality := range order {
		details = append(details, &ModalityTokenCount{Modality: modality, TokenCount: byModality[modality]})
	}

	return details
}

func fileModality(contentType string) string {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "IMAGE"
	case strings.HasPrefix(contentType, "audio/"):
		return "AUDIO"
	case strings.HasPrefix(contentType, "video/"):
		return "VIDEO"
	case contentType == "application/pdf":
		return "DOCUMENT"
	}

	return "TEXT"
}
