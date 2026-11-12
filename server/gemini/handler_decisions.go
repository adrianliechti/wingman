package gemini

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
)

func (h *Handler) handleDecisions(w http.ResponseWriter, r *http.Request) {
	var req google.DecisionRequest
	// Decisions uses Interactions JSON, not generateContent's protobuf JSON.
	// In particular, question and option keys must never be camel-cased.
	if err := jsonutil.UnmarshalRead(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	input, err := req.ProviderInput()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	model, p, err := h.Decider(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	result, err := p.Decide(r.Context(), input)
	if err != nil {
		// Decisions safety errors use a string code rather than the numeric
		// Google RPC code used by generateContent.
		switch code := provider.TypeFromError(err); code {
		case "safety", "prohibited_content", "blocklist", "image_safety", "content_blocked":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(provider.CodeFromError(err, http.StatusBadRequest))
			writeJson(w, map[string]any{"error": map[string]string{"code": code, "message": err.Error()}})
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	response, err := google.DecisionResponseFromProvider(result)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJson(w, response)
}
