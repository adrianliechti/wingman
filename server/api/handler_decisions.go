package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type DecisionsRequest struct {
	Model     string                      `json:"model"`
	State     json.RawMessage             `json:"state"`
	Questions map[string]DecisionQuestion `json:"questions"`
}

type DecisionQuestion struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

type DecisionsResponse struct {
	ID      string                    `json:"id,omitempty"`
	Model   string                    `json:"model"`
	Answers map[string]DecisionAnswer `json:"answers"`
	Usage   DecisionsUsage            `json:"usage"`
}

// Optional pointers preserve zero-valued answers while omitting fields that
// belong to another primitive. The provider layer uses separate answer types.
type DecisionAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]any     `json:"legend,omitempty"`
}

type DecisionsUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (h *Handler) handleDecisions(w http.ResponseWriter, r *http.Request) {
	var req DecisionsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		writeDecisionError(w, http.StatusBadRequest, err)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeDecisionError(w, http.StatusBadRequest, errors.New("request must contain a single JSON object"))
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeDecisionError(w, http.StatusBadRequest, errors.New("model is required"))
		return
	}
	input, err := req.input()
	if err != nil {
		writeDecisionError(w, http.StatusBadRequest, err)
		return
	}
	p, err := h.Decider(req.Model)
	if err != nil {
		writeDecisionError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeDecisionError(w, http.StatusNotFound, err)
		return
	}

	result, err := p.Decide(r.Context(), input)
	if err != nil {
		writeDecisionError(w, provider.CodeFromError(err, http.StatusBadGateway), err)
		return
	}
	if result == nil {
		writeDecisionError(w, http.StatusBadGateway, errors.New("empty decision response"))
		return
	}
	writeJson(w, decisionsResponse(result))
}

func (r DecisionsRequest) input() (*provider.DecisionInput, error) {
	if len(r.State) == 0 {
		return nil, errors.New("state is required")
	}
	var state any
	decoder := json.NewDecoder(bytes.NewReader(r.State))
	decoder.UseNumber()
	if err := decoder.Decode(&state); err != nil {
		return nil, err
	}
	input := &provider.DecisionInput{}
	switch state := state.(type) {
	case map[string]any:
		input.State = state
	case string:
		input.State = map[string]any{"text": state}
	case []any:
		input.State = map[string]any{"items": state}
	case nil:
	default:
		return nil, errors.New("state must be a string, object, array, or null")
	}

	for _, id := range slices.Sorted(maps.Keys(r.Questions)) {
		question := r.Questions[id]
		q := provider.DecisionQuestion{ID: id, Instructions: question.Instructions}
		switch question.Type {
		case "noul":
			q.Noul = &provider.NoulQuestion{}
			if question.Criteria != nil {
				criteria, ok := question.Criteria.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("questions[%q]: noul criteria must be an object", id)
				}
				for key := range criteria {
					if key != "true" && key != "false" {
						return nil, fmt.Errorf("questions[%q]: noul criteria only supports true and false", id)
					}
				}
				q.Noul.True, q.Noul.False = criteria["true"], criteria["false"]
			}
		case "choice":
			criteria, ok := question.Criteria.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("questions[%q]: choice criteria must be an object", id)
			}
			q.Choice = &provider.ChoiceQuestion{}
			for _, label := range slices.Sorted(maps.Keys(criteria)) {
				q.Choice.Options = append(q.Choice.Options, provider.DecisionOption{Label: label, Description: criteria[label]})
			}
		case "score":
			criteria, ok := question.Criteria.([]any)
			if !ok {
				return nil, fmt.Errorf("questions[%q]: score criteria must be an array", id)
			}
			q.Score = &provider.ScoreQuestion{Levels: criteria}
		default:
			return nil, fmt.Errorf("questions[%q]: unsupported type %q", id, question.Type)
		}
		input.Questions = append(input.Questions, q)
	}
	return input, input.Validate()
}

func decisionsResponse(result *provider.Decision) DecisionsResponse {
	response := DecisionsResponse{
		ID: result.ID, Model: result.Model,
		Answers: make(map[string]DecisionAnswer, len(result.Answers)),
	}
	if result.Usage != nil {
		response.Usage.InputTokens = result.Usage.InputTokens
		response.Usage.OutputTokens = result.Usage.OutputTokens
	}
	for _, answer := range result.Answers {
		var value DecisionAnswer
		switch {
		case answer.Noul != nil:
			value.Type, value.Noul = "noul", &answer.Noul.Probability
		case answer.Choice != nil:
			value.Type, value.Choice = "choice", &answer.Choice.Choice
			value.Confidence = &answer.Choice.Confidence
			value.Probabilities = answer.Choice.Probabilities
		case answer.Score != nil:
			value.Type, value.Score = "score", &answer.Score.Score
			value.Confidence = &answer.Score.Confidence
			value.Probabilities = make(map[string]float64, len(answer.Score.Levels))
			value.Legend = make(map[string]any, len(answer.Score.Levels))
			for i, level := range answer.Score.Levels {
				key := strconv.Itoa(i)
				value.Probabilities[key], value.Legend[key] = level.Probability, level.Description
			}
		}
		response.Answers[answer.ID] = value
	}
	return response
}

func writeDecisionError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	if retry := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": err.Error(), "type": provider.TypeFromError(err), "code": code},
	})
}
