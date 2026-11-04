package api

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type DecisionsRequest struct {
	Model     string                      `json:"model"`
	State     jsontext.Value              `json:"state"`
	Questions map[string]DecisionQuestion `json:"questions"`
	Assets    map[string]DecisionAsset    `json:"assets,omitempty"`
}

type DecisionQuestion struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions,omitzero"`
	Criteria     any    `json:"criteria,omitzero"`
}

type DecisionAsset struct {
	Type       string `json:"type"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
	Part       string `json:"part"`
}

type DecisionsResponse struct {
	ID      string                    `json:"id,omitzero"`
	Model   string                    `json:"model"`
	Answers map[string]DecisionAnswer `json:"answers"`
	Usage   DecisionsUsage            `json:"usage"`
}

// Optional pointers preserve zero-valued answers while omitting fields that
// belong to another primitive. The provider layer uses separate answer types.
type DecisionAnswer struct {
	Type          string                   `json:"type"`
	Message       string                   `json:"message,omitzero"`
	Noul          *float64                 `json:"noul,omitzero"`
	Choice        *string                  `json:"choice,omitzero"`
	Score         *float64                 `json:"score,omitzero"`
	Confidence    *float64                 `json:"confidence,omitzero"`
	Probabilities map[string]float64       `json:"probabilities,omitempty"`
	Legend        map[string]any           `json:"legend,omitempty"`
	Box           *provider.BoxCoordinates `json:"box,omitzero"`
}

func (a DecisionAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	type plain DecisionAnswer
	if a.Type == "box" {
		return json.MarshalEncode(enc, struct {
			Type       string                   `json:"type"`
			Box        *provider.BoxCoordinates `json:"box"`
			Confidence *float64                 `json:"confidence"`
		}{a.Type, a.Box, a.Confidence})
	}
	return json.MarshalEncode(enc, plain(a))
}

type DecisionsUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (h *Handler) handleDecisions(w http.ResponseWriter, r *http.Request) {
	req, err := readDecisionsRequest(r)
	if err != nil {
		writeDecisionError(w, http.StatusBadRequest, err)
		return
	}
	input, err := req.input()
	if err != nil {
		writeDecisionError(w, http.StatusBadRequest, err)
		return
	}
	model, p, err := h.Decider(req.Model)
	if err != nil {
		writeDecisionError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
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
	if err := jsonutil.Unmarshal(r.State, &state); err != nil {
		return nil, err
	}
	input := &provider.DecisionInput{State: state}
	if len(r.Assets) > 0 {
		input.Assets = make(map[string]*provider.File, len(r.Assets))
		for key, asset := range r.Assets {
			if key == "" || asset.Type != "image" || asset.Part != "" {
				return nil, errors.New("assets require a nonempty placeholder, type image, and inline image data")
			}
			if asset.Format != "png" && asset.Format != "jpg" && asset.Format != "jpeg" {
				return nil, errors.New("asset format must be png, jpg, or jpeg")
			}
			data, err := base64.StdEncoding.DecodeString(asset.DataBase64)
			if err != nil || len(data) == 0 {
				return nil, errors.New("invalid asset image data")
			}
			mediaType := "image/" + asset.Format
			if asset.Format == "jpg" {
				mediaType = "image/jpeg"
			}
			input.Assets[key] = &provider.File{Content: data, ContentType: mediaType}
		}
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
			if !ok || len(criteria) < 1 || len(criteria) > 10 {
				return nil, fmt.Errorf("questions[%q]: score criteria must be an array of 1 to 10 items", id)
			}
			q.Score = &provider.ScoreQuestion{Levels: criteria}
		case "box":
			if question.Criteria != nil {
				return nil, fmt.Errorf("questions[%q]: box does not accept criteria", id)
			}
			if len(input.Assets) == 0 {
				return nil, errors.New("box questions require an image asset")
			}
			q.Box = &provider.BoxQuestion{}
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
		case answer.Refusal != nil:
			value.Type = "refusal"
		case answer.Error != nil:
			value.Type, value.Message = "error", answer.Error.Message
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
		case answer.Box != nil:
			value.Type = "box"
			value.Box = &answer.Box.Box
			value.Confidence = answer.Box.Confidence
		}
		response.Answers[answer.ID] = value
	}
	return response
}

func writeDecisionError(w http.ResponseWriter, code int, err error) {
	if retry := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	data, encodeErr := json.Marshal(map[string]any{"error": map[string]any{"message": err.Error(), "type": provider.TypeFromError(err), "code": code}})
	if encodeErr != nil {
		code = http.StatusInternalServerError
		data = []byte(`{"error":{"message":"could not encode error response","type":"server_error","code":500}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

func readDecisionsRequest(r *http.Request) (DecisionsRequest, error) {
	var request DecisionsRequest
	decode := func(reader io.Reader) error {
		return jsonutil.UnmarshalRead(reader, &request)
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		err := decode(r.Body)
		return request, err
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return request, err
	}
	first, err := reader.NextPart()
	if err != nil {
		return request, err
	}
	if first.FormName() != "request" {
		return request, errors.New("first multipart part must be request")
	}
	if err := decode(first); err != nil {
		return request, err
	}
	images := map[string][]byte{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return request, err
		}
		name := part.FormName()
		if name == "" || name == "request" || images[name] != nil {
			return request, errors.New("image part names must be unique and nonempty")
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return request, err
		}
		if len(data) == 0 {
			return request, errors.New("image parts must not be empty")
		}
		images[name] = data
	}
	used := map[string]bool{}
	for key, asset := range request.Assets {
		if asset.Part == "" {
			continue
		}
		data, ok := images[asset.Part]
		if !ok || used[asset.Part] {
			return request, errors.New("each image part must be used by exactly one asset")
		}
		used[asset.Part] = true
		asset.Part = ""
		asset.DataBase64 = base64.StdEncoding.EncodeToString(data)
		switch http.DetectContentType(data) {
		case "image/png":
			asset.Format = "png"
		case "image/jpeg":
			asset.Format = "jpeg"
		default:
			return request, errors.New("image parts must contain PNG or JPEG images")
		}
		request.Assets[key] = asset
	}
	if len(used) != len(images) {
		return request, errors.New("unused multipart image part")
	}
	return request, nil
}
