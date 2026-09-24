package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type Decider interface {
	Decide(ctx context.Context, input *DecisionInput) (*Decision, error)
}

type DecisionInput struct {
	State     map[string]any
	Questions []DecisionQuestion
}

// DecisionQuestion contains exactly one question variant. Instructions and
// descriptions may be text, structured objects, arrays, or nil.
type DecisionQuestion struct {
	ID           string
	Instructions any

	Noul   *NoulQuestion
	Choice *ChoiceQuestion
	Score  *ScoreQuestion
}

type NoulQuestion struct {
	True  any
	False any
}

type ChoiceQuestion struct {
	Options []DecisionOption
}

type DecisionOption struct {
	Label       string
	Description any
}

type ScoreQuestion struct {
	Levels []any
}

type Decision struct {
	ID      string
	Model   string
	Answers []DecisionAnswer
	Usage   *Usage
}

// DecisionAnswer contains exactly one typed answer, matching its question.
type DecisionAnswer struct {
	ID string

	Noul   *NoulAnswer
	Choice *ChoiceAnswer
	Score  *ScoreAnswer
}

type NoulAnswer struct {
	Probability float64
}

type ChoiceAnswer struct {
	Choice        string
	Probabilities map[string]float64
	Confidence    float64
}

type ScoreAnswer struct {
	Score      float64
	Levels     []ScoreLevel
	Confidence float64
}

type ScoreLevel struct {
	Description any
	Probability float64
}

func (input *DecisionInput) Validate() error {
	if input == nil {
		return errors.New("decision input is required")
	}
	if _, err := json.Marshal(input.State); err != nil {
		return fmt.Errorf("invalid state: %w", err)
	}
	if len(input.Questions) == 0 {
		return errors.New("questions must contain at least one question")
	}
	seen := make(map[string]bool, len(input.Questions))
	for _, q := range input.Questions {
		if seen[q.ID] {
			return fmt.Errorf("duplicate question id: %q", q.ID)
		}
		seen[q.ID] = true
		if err := q.validate(); err != nil {
			return fmt.Errorf("questions[%q]: %w", q.ID, err)
		}
	}
	return nil
}

func (q DecisionQuestion) validate() error {
	variants := 0
	var descriptions []any
	if q.Noul != nil {
		variants++
		descriptions = append(descriptions, q.Noul.True, q.Noul.False)
	}
	if q.Choice != nil {
		variants++
		if len(q.Choice.Options) < 1 || len(q.Choice.Options) > 255 {
			return errors.New("choice requires 1 to 255 options")
		}
		seen := make(map[string]bool)
		for _, option := range q.Choice.Options {
			if seen[option.Label] {
				return fmt.Errorf("duplicate choice option: %q", option.Label)
			}
			seen[option.Label] = true
			descriptions = append(descriptions, option.Description)
		}
	}
	if q.Score != nil {
		variants++
		if len(q.Score.Levels) < 2 || len(q.Score.Levels) > 10 {
			return errors.New("score requires 2 to 10 levels")
		}
		descriptions = append(descriptions, q.Score.Levels...)
	}
	if variants != 1 {
		return errors.New("exactly one of noul, choice, or score is required")
	}
	if !decisionEntry(q.Instructions) {
		return errors.New("instructions must be text, an object, an array, or null")
	}
	for _, description := range descriptions {
		if !decisionEntry(description) {
			return errors.New("criteria descriptions must be text, objects, arrays, or null")
		}
	}
	return nil
}

func decisionEntry(value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return data[0] == '"' || data[0] == '{' || data[0] == '[' || string(data) == "null"
}
