package decisions

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"
)

// https://developers.openai.com/api/reference/resources/decisions/methods/create
type Request struct {
	Model              string                     `json:"model"`
	Input              jsontext.Value             `json:"input"`
	Questions          []Question                 `json:"questions"`
	PromptCacheKey     *string                    `json:"prompt_cache_key,omitzero"`
	PromptCacheOptions *shared.PromptCacheOptions `json:"prompt_cache_options,omitzero"`
}

type Question struct {
	Type         string         `json:"type"`
	ID           *string        `json:"id,omitzero"`
	Name         *string        `json:"name,omitzero"`
	Instructions jsontext.Value `json:"instructions"`
	Choices      []Choice       `json:"choices,omitempty"`
	Levels       []Level        `json:"levels,omitempty"`
}

type Choice struct {
	Value       any    `json:"value"`
	Description string `json:"description,omitzero"`
}

type Level struct {
	Value       *int   `json:"value,omitzero"`
	Label       string `json:"label"`
	Description string `json:"description,omitzero"`
}

func (l *Level) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var value struct {
		Value       *int    `json:"value"`
		Label       *string `json:"label"`
		Description string  `json:"description"`
	}
	if err := json.UnmarshalDecode(dec, &value); err != nil {
		return err
	}
	if value.Label == nil && value.Value == nil {
		return errors.New("score level label or value is required")
	}
	*l = Level{Value: value.Value, Description: value.Description}
	if value.Label != nil {
		l.Label = *value.Label
	}
	return nil
}

type Response struct {
	Model   string   `json:"model"`
	Answers []Answer `json:"answers"`
	Usage   Usage    `json:"usage"`
}

type Answer struct {
	Type          string        `json:"type"`
	ID            *string       `json:"id,omitzero"`
	Name          *string       `json:"name"`
	Message       string        `json:"message,omitzero"`
	Probability   *float64      `json:"probability,omitzero"`
	Choice        any           `json:"choice,omitzero"`
	Confidence    *float64      `json:"confidence,omitzero"`
	Score         *float64      `json:"score,omitzero"`
	Probabilities []Probability `json:"probabilities,omitempty"`
}

type Probability struct {
	Value       any      `json:"value"`
	Label       *string  `json:"label,omitzero"`
	Probability *float64 `json:"probability"`
}

type Usage struct {
	InputTokens        int `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens            int                             `json:"cached_tokens"`
		CacheWriteTokens        int                             `json:"cache_write_tokens"`
		CacheWriteTokensDetails *shared.CacheWriteTokensDetails `json:"cache_write_tokens_details,omitzero"`
	} `json:"input_tokens_details"`
	OutputTokens        int `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens int `json:"total_tokens"`
}

// ProviderInput validates the supported wire variants before adapting them.
func (r Request) ProviderInput() (*provider.DecisionInput, error) {
	messages, err := parseInput(r.Input)
	if err != nil {
		return nil, err
	}
	input := &provider.DecisionInput{State: r.Input, Messages: messages}
	if err := r.PromptCacheOptions.Validate(); err != nil {
		return nil, err
	}
	if r.PromptCacheKey != nil && utf8.RuneCountInString(*r.PromptCacheKey) > 64 {
		return nil, errors.New("prompt_cache_key must be at most 64 characters")
	}
	input.CacheOptions = shared.CacheOptions(r.PromptCacheKey, nil, r.PromptCacheOptions)
	if len(r.Questions) > 400 {
		return nil, errors.New("at most 400 questions are supported")
	}
	images := 0
	for _, m := range messages {
		for _, c := range m.Content {
			if c.File != nil {
				images++
			}
			if c.ToolResult != nil {
				for _, p := range c.ToolResult.Parts {
					if p.File != nil {
						images++
					}
				}
			}
		}
	}
	seenIDs := make(map[string]bool)
	for i, q := range r.Questions {
		if q.ID != nil {
			if *q.ID == "" {
				return nil, fmt.Errorf("questions[%d].id: must not be empty", i)
			}
			if seenIDs[*q.ID] {
				return nil, fmt.Errorf("questions[%d].id: duplicate question id %q", i, *q.ID)
			}
			seenIDs[*q.ID] = true
		}
		instructions, err := parseQuestionInstructions(q.Instructions)
		if err != nil {
			return nil, fmt.Errorf("questions[%d].instructions: %w", i, err)
		}
		if parts, ok := instructions.([]provider.Content); ok {
			for _, c := range parts {
				if c.File != nil {
					images++
				}
			}
		}
		question := provider.DecisionQuestion{ID: strconv.Itoa(i), Instructions: instructions}
		if _, ok := instructions.(string); ok {
			question.Instructions = q.Instructions
		}
		switch q.Type {
		case "predicate", "binary":
			if len(q.Choices) > 0 || len(q.Levels) > 0 {
				return nil, fmt.Errorf("%s does not accept choices or levels", q.Type)
			}
			question.Noul = &provider.NoulQuestion{}
		case "choice":
			if len(q.Levels) > 0 {
				return nil, errors.New("choice does not accept levels")
			}
			question.Choice = &provider.ChoiceQuestion{}
			seen := make(map[any]bool)
			for j, c := range q.Choices {
				switch c.Value.(type) {
				case string, bool:
				default:
					return nil, errors.New("choice value must be a string or boolean")
				}
				if seen[c.Value] {
					return nil, errors.New("duplicate choice value")
				}
				seen[c.Value] = true
				question.Choice.Options = append(question.Choice.Options, provider.DecisionOption{Label: strconv.Itoa(j), Value: c.Value, Description: c.Description})
			}
		case "score":
			if len(q.Choices) > 0 {
				return nil, errors.New("score does not accept choices")
			}
			question.Score = &provider.ScoreQuestion{}
			hasCustomValues := false
			for _, l := range q.Levels {
				if l.Value != nil {
					hasCustomValues = true
					break
				}
			}
			if len(q.Levels) < 2 {
				return nil, errors.New("score requires 2 to 10 levels")
			}
			for j, l := range q.Levels {
				question.Score.Levels = append(question.Score.Levels, l.Description)
				question.Score.Labels = append(question.Score.Labels, l.Label)
				if hasCustomValues {
					val := j
					if l.Value != nil {
						val = *l.Value
					}
					question.Score.Values = append(question.Score.Values, val)
				}
			}
		default:
			return nil, fmt.Errorf("questions[%d]: unsupported type %q", i, q.Type)
		}
		input.Questions = append(input.Questions, question)
	}
	if images > 128 {
		return nil, errors.New("at most 128 images are supported per request")
	}
	return input, input.Validate()
}
