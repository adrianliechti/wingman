package typesafe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Decider = (*Decider)(nil)

type Decider struct {
	*Config
}

// NewDecider accepts a complete evaluation endpoint, including its path.
// An empty endpoint uses TypeSafe's hosted System One API.
func NewDecider(endpoint, model string, options ...Option) (*Decider, error) {
	if endpoint == "" {
		endpoint = "https://api.typesafe.ai/v1/systemone"
	}
	cfg := &Config{endpoint: endpoint, model: model, maxRetries: 2}
	for _, option := range options {
		option(cfg)
	}
	if cfg.client == nil {
		cfg.client = provider.DefaultClient
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("decision model is required")
	}
	return &Decider{Config: cfg}, nil
}

type decisionRequest struct {
	Model     string                      `json:"model"`
	State     any                         `json:"state"`
	Questions map[string]decisionQuestion `json:"questions"`
	Assets    map[string]decisionAsset    `json:"assets,omitempty"`
}

type decisionQuestion struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitzero"`
}

type decisionAsset struct {
	Type       string `json:"type"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
}

type decisionResponse struct {
	ID      string                    `json:"id"`
	Model   string                    `json:"model"`
	Answers map[string]decisionAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type decisionAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]any     `json:"legend"`
	Box           *struct {
		Top    *float64 `json:"top"`
		Bottom *float64 `json:"bottom"`
		Left   *float64 `json:"left"`
		Right  *float64 `json:"right"`
	} `json:"box"`
}

func (a *decisionAnswer) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	type plain decisionAnswer
	var wire struct {
		plain
		Confidence jsontext.Value `json:"confidence"`
	}
	if err := json.UnmarshalDecode(dec, &wire, jsonutil.PreserveNumbers); err != nil {
		return err
	}
	if wire.Type == "box" && len(wire.Confidence) == 0 {
		return errors.New("box confidence is required (may be null)")
	}
	if len(wire.Confidence) > 0 {
		if err := json.Unmarshal(wire.Confidence, &wire.plain.Confidence, dec.Options()); err != nil {
			return err
		}
	}
	*a = decisionAnswer(wire.plain)
	return nil
}

func (d *Decider) Decide(ctx context.Context, input *provider.DecisionInput) (*provider.Decision, error) {
	if err := input.Validate(); err != nil {
		return nil, provider.InvalidRequest(err)
	}
	assets := make(map[string]decisionAsset)
	for key, file := range input.Assets {
		asset, err := imageAsset(file)
		if err != nil {
			return nil, provider.InvalidRequest(err)
		}
		assets[key] = asset
	}
	var encodingError error
	encodeParts := func(parts []provider.Content) any {
		var out []any
		for _, c := range parts {
			if c.Text != "" || c.TextPresent {
				out = append(out, c.Text)
			}
			if c.File != nil {
				key := uuid.NewString()
				asset, err := imageAsset(c.File)
				if err != nil {
					encodingError = err
					continue
				}
				assets[key] = asset
				out = append(out, key)
			}
		}
		if len(out) == 1 {
			return out[0]
		}
		return out
	}
	encodeValue := func(v any) any {
		if parts, ok := v.([]provider.Content); ok {
			return encodeParts(parts)
		}
		return v
	}
	state := encodeValue(input.State)
	if len(input.Messages) > 0 {
		var items []any
		for _, m := range input.Messages {
			var contents []any
			for _, c := range m.Content {
				if c.ToolCall != nil {
					contents = append(contents, map[string]any{"type": "tool_call", "kind": c.ToolCall.Kind, "call_id": c.ToolCall.ID, "name": c.ToolCall.Name, "arguments": c.ToolCall.Arguments})
					continue
				}
				if c.ToolResult != nil {
					var parts []provider.Content
					for _, part := range c.ToolResult.Parts {
						parts = append(parts, provider.Content{Text: part.Text, File: part.File})
					}
					contents = append(contents, map[string]any{"type": "tool_output", "call_id": c.ToolResult.ID, "output": encodeParts(parts)})
					continue
				}
				if c.Refusal != "" {
					contents = append(contents, map[string]any{"refusal": c.Refusal})
				}
				if c.Text != "" || c.TextPresent || c.File != nil {
					contents = append(contents, encodeParts([]provider.Content{c}))
				}
			}
			items = append(items, map[string]any{"role": m.Role, "content": contents})
		}
		state = items
	}
	body := decisionRequest{Model: d.model, State: state, Assets: assets, Questions: make(map[string]decisionQuestion, len(input.Questions))}
	for _, q := range input.Questions {
		instructions := encodeValue(q.Instructions)
		question := decisionQuestion{Instructions: instructions}
		switch {
		case q.Noul != nil:
			question.Type = "noul"
			criteria := make(map[string]any)
			if q.Noul.True != nil {
				criteria["true"] = encodeValue(q.Noul.True)
			}
			if q.Noul.False != nil {
				criteria["false"] = encodeValue(q.Noul.False)
			}
			if len(criteria) > 0 {
				question.Criteria = criteria
			}
		case q.Choice != nil:
			question.Type = "choice"
			criteria := make(map[string]any, len(q.Choice.Options))
			for _, option := range q.Choice.Options {
				criteria[option.Label] = encodeValue(option.Description)
			}
			question.Criteria = criteria
		case q.Score != nil:
			if len(q.Score.Levels) > 10 {
				return nil, provider.InvalidRequest(errors.New("TypeSafe score questions support at most 10 levels"))
			}
			question.Type = "score"
			levels := make([]any, len(q.Score.Levels))
			for i, v := range q.Score.Levels {
				levels[i] = encodeValue(v)
			}
			question.Criteria = levels
		case q.Box != nil:
			question.Type = "box"
		}
		body.Questions[q.ID] = question
	}
	if encodingError != nil {
		return nil, provider.InvalidRequest(encodingError)
	}
	// Opaque state and criteria become model context; keep their order stable.
	data, err := json.Marshal(body, json.Deterministic(true))
	if err != nil {
		return nil, provider.InvalidRequest(err)
	}

	var response *decisionResponse
	for attempt := 0; ; attempt++ {
		response, err = d.evaluate(ctx, data, len(assets) > 0 || hasBoxQuestion(input))
		if err == nil || attempt >= d.maxRetries {
			break
		}
		var upstream *provider.ProviderError
		if !errors.As(err, &upstream) || !(upstream.Code == 408 || upstream.Code == 429 || upstream.Code >= 500) {
			break
		}
		delay := upstream.RetryAfter
		if delay <= 0 {
			delay = time.Duration(1<<min(attempt, 4)) * 500 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		return nil, err
	}
	return response.decision(input)
}

func (d *Decider) evaluate(ctx context.Context, data []byte, images bool) (*decisionResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if images {
		req.Header.Set("TypeSafe-Danger-Zone-Images", "true")
	}
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		upstream := &provider.ProviderError{Code: resp.StatusCode, Message: string(data)}
		var envelope struct {
			Detail jsontext.Value `json:"detail"`
			Error  struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil {
			if len(envelope.Detail) > 0 && string(envelope.Detail) != "null" {
				detail := &APIError{Detail: envelope.Detail}
				upstream.Message, upstream.Type, upstream.Err = detail.Error(), detail.errorType(), detail
			} else if envelope.Error.Message != "" {
				upstream.Message, upstream.Type = envelope.Error.Message, envelope.Error.Type
			}
		}
		if upstream.Message == "" {
			upstream.Message = fmt.Sprintf("decision API returned HTTP %d", resp.StatusCode)
		}
		if seconds, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && seconds > 0 && !math.IsInf(seconds, 0) {
			upstream.RetryAfter = time.Duration(seconds * float64(time.Second))
		} else if deadline, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			upstream.RetryAfter = max(0, time.Until(deadline))
		}
		return nil, upstream
	}

	var result decisionResponse
	if err := jsonutil.UnmarshalRead(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("invalid decision response: %w", err)
	}
	return &result, nil
}

func (r *decisionResponse) decision(input *provider.DecisionInput) (*provider.Decision, error) {
	if r.Model == "" || len(r.Answers) != len(input.Questions) {
		return nil, errors.New("decision response must contain a model and every question exactly once")
	}
	result := &provider.Decision{
		ID: r.ID, Model: r.Model,
		Usage: &provider.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens},
	}
	for _, q := range input.Questions {
		answer, ok := r.Answers[q.ID]
		if !ok {
			return nil, fmt.Errorf("decision response is missing question %q", q.ID)
		}
		value := provider.DecisionAnswer{ID: q.ID}
		switch {
		case q.Noul != nil:
			if answer.Type != "noul" || !probability(answer.Noul) {
				return nil, fmt.Errorf("question %q: invalid noul answer", q.ID)
			}
			value.Noul = &provider.NoulAnswer{Probability: *answer.Noul}
		case q.Choice != nil:
			if answer.Type != "choice" || answer.Choice == nil || !probability(answer.Confidence) || len(answer.Probabilities) != len(q.Choice.Options) {
				return nil, fmt.Errorf("question %q: invalid choice answer", q.ID)
			}
			if _, ok := answer.Probabilities[*answer.Choice]; !ok {
				return nil, fmt.Errorf("question %q: unknown selected choice", q.ID)
			}
			for _, option := range q.Choice.Options {
				p, ok := answer.Probabilities[option.Label]
				if !ok || !probability(&p) {
					return nil, fmt.Errorf("question %q: invalid probability for choice %q", q.ID, option.Label)
				}
			}
			value.Choice = &provider.ChoiceAnswer{
				Choice: *answer.Choice, Confidence: *answer.Confidence, Probabilities: answer.Probabilities,
			}
		case q.Score != nil:
			if answer.Type != "score" || answer.Score == nil || *answer.Score < 0 || *answer.Score > float64(len(q.Score.Levels)-1) || !probability(answer.Confidence) ||
				len(answer.Probabilities) != len(q.Score.Levels) || len(answer.Legend) != len(q.Score.Levels) {
				return nil, fmt.Errorf("question %q: invalid score answer", q.ID)
			}
			score := &provider.ScoreAnswer{Score: *answer.Score, Confidence: *answer.Confidence}
			var customScore float64
			for i := range q.Score.Levels {
				key := strconv.Itoa(i)
				p, ok := answer.Probabilities[key]
				description, exists := answer.Legend[key]
				if !ok || !exists || !probability(&p) {
					return nil, fmt.Errorf("question %q: invalid score level %q", q.ID, key)
				}
				val := i
				if i < len(q.Score.Values) {
					val = q.Score.Values[i]
				}
				var label string
				if i < len(q.Score.Labels) {
					label = q.Score.Labels[i]
				}
				customScore += float64(val) * p
				score.Levels = append(score.Levels, provider.ScoreLevel{
					Value:       val,
					Label:       label,
					Description: description,
					Probability: p,
				})
			}
			if len(q.Score.Values) > 0 {
				score.Score = customScore
			}
			value.Score = score
		case q.Box != nil:
			if answer.Type != "box" || answer.Box == nil || answer.Box.Top == nil || answer.Box.Bottom == nil || answer.Box.Left == nil || answer.Box.Right == nil {
				return nil, fmt.Errorf("question %q: invalid box answer", q.ID)
			}
			coords := provider.BoxCoordinates{Top: *answer.Box.Top, Bottom: *answer.Box.Bottom, Left: *answer.Box.Left, Right: *answer.Box.Right}
			if err := coords.Validate(answer.Confidence); err != nil {
				return nil, fmt.Errorf("question %q: %w", q.ID, err)
			}
			value.Box = &provider.BoxAnswer{Box: coords, Confidence: answer.Confidence}
		}
		result.Answers = append(result.Answers, value)
	}
	return result, nil
}

func probability(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1
}

func imageAsset(file *provider.File) (decisionAsset, error) {
	format := ""
	switch file.ContentType {
	case "image/png":
		format = "png"
	case "image/jpeg":
		format = "jpeg"
	default:
		return decisionAsset{}, errors.New("TypeSafe assets require PNG or JPEG images")
	}
	return decisionAsset{Type: "image", Format: format, DataBase64: base64.StdEncoding.EncodeToString(file.Content)}, nil
}

func hasBoxQuestion(input *provider.DecisionInput) bool {
	for _, q := range input.Questions {
		if q.Box != nil {
			return true
		}
	}
	return false
}
