package decisions

import (
	"net/http"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"

	"github.com/go-chi/chi/v5"
)

type Handler struct{ *config.Config }

func New(cfg *config.Config) *Handler { return &Handler{Config: cfg} }

func (h *Handler) Attach(r chi.Router) { r.Post("/decisions", h.handleDecisions) }

func (h *Handler) handleDecisions(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := jsonutil.UnmarshalRead(r.Body, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}
	input, err := req.ProviderInput()
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}
	model, p, err := h.Decider(req.Model)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}
	req.Model = model
	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		shared.WriteError(w, http.StatusNotFound, err)
		return
	}
	result, err := p.Decide(r.Context(), input)
	if err != nil {
		shared.WriteError(w, provider.CodeFromError(err, http.StatusBadGateway), err)
		return
	}
	response, err := FromDecision(req, input, result)
	if err != nil {
		shared.WriteError(w, http.StatusBadGateway, err)
		return
	}
	shared.WriteJson(w, response)
}
