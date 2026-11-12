package provider

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
)

type Decider interface {
	Decide(ctx context.Context, input *DecisionInput) (*Decision, error)
}

type DecisionInput struct {
	State    any
	Messages []Message
	// Assets replace matching placeholders in state, instructions, and criteria.
	Assets       map[string]*File
	Questions    []DecisionQuestion
	CacheOptions *CacheOptions
}

// DecisionQuestion contains exactly one question variant. Instructions and
// descriptions may be text, structured objects, arrays, []Content parts, or nil.
type DecisionQuestion struct {
	ID           string
	Instructions any

	Noul   *NoulQuestion
	Choice *ChoiceQuestion
	Score  *ScoreQuestion
	Box    *BoxQuestion
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
	Value       any
}

type ScoreQuestion struct {
	Levels []any
	Labels []string
	Values []int
}

type BoxQuestion struct{}

type Decision struct {
	ID      string
	Model   string
	Answers []DecisionAnswer
	Usage   *Usage
}

// DecisionAnswer contains exactly one typed answer, matching its question.
type DecisionAnswer struct {
	ID string

	Noul    *NoulAnswer
	Choice  *ChoiceAnswer
	Score   *ScoreAnswer
	Box     *BoxAnswer
	Refusal *RefusalAnswer
	Error   *ErrorAnswer
}

type RefusalAnswer struct{}

type ErrorAnswer struct {
	Message string
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
	Value       int
	Label       string
	Description any
	Probability float64
}

type BoxAnswer struct {
	Box        BoxCoordinates
	Confidence *float64
}

type BoxCoordinates struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}

func (input *DecisionInput) Validate() error {
	if input == nil {
		return errors.New("decision input is required")
	}
	if len(input.Messages) > 0 {
		for i, m := range input.Messages {
			if err := validateDecisionMessage(m); err != nil {
				return fmt.Errorf("messages[%d]: %w", i, err)
			}
		}
	} else {
		if _, err := json.Marshal(input.State, json.Deterministic(true)); err != nil {
			return fmt.Errorf("invalid state: %w", err)
		}
		if !decisionEntry(input.State) {
			return errors.New("state must be a string, object, array, or null")
		}
	}
	for key, file := range input.Assets {
		if key == "" || file == nil || len(file.Content) == 0 || !file.ImageDetail.Valid() {
			return errors.New("assets require nonempty placeholders and image content")
		}
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
			switch option.Value.(type) {
			case nil, string, bool:
			default:
				return errors.New("choice values must be strings or booleans")
			}
			if seen[option.Label] {
				return fmt.Errorf("duplicate choice option: %q", option.Label)
			}
			seen[option.Label] = true
			descriptions = append(descriptions, option.Description)
		}
	}
	if q.Score != nil {
		variants++
		if q.Score.Labels != nil && len(q.Score.Labels) != len(q.Score.Levels) {
			return errors.New("score labels must match the level count")
		}
		if q.Score.Values != nil && len(q.Score.Values) != len(q.Score.Levels) {
			return errors.New("score values must match the level count")
		}
		if len(q.Score.Levels) < 1 || len(q.Score.Levels) > 255 {
			return errors.New("score requires 1 to 255 levels")
		}
		seenValues := make(map[int]bool, len(q.Score.Values))
		for _, v := range q.Score.Values {
			if seenValues[v] {
				return fmt.Errorf("duplicate score level value: %d", v)
			}
			seenValues[v] = true
		}
		descriptions = append(descriptions, q.Score.Levels...)
	}
	if q.Box != nil {
		variants++
	}
	if variants != 1 {
		return errors.New("exactly one of noul, choice, score, or box is required")
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

func validateDecisionMessage(m Message) error {
	if m.Role != MessageRoleUser && m.Role != MessageRoleAssistant && m.Role != MessageRoleSystem {
		return errors.New("unsupported decision message role")
	}
	for _, c := range m.Content {
		if c.File != nil {
			if len(c.File.Content) == 0 {
				return errors.New("file content is empty")
			}
			if !c.File.ImageDetail.Valid() {
				return errors.New("invalid image detail")
			}
		}
		if c.ToolResult != nil {
			for _, p := range c.ToolResult.Parts {
				if p.File != nil {
					if len(p.File.Content) == 0 {
						return errors.New("tool output file content is empty")
					}
					if !p.File.ImageDetail.Valid() {
						return errors.New("invalid tool output image detail")
					}
				}
			}
		}
	}
	return nil
}

func decisionEntry(value any) bool {
	if parts, ok := value.([]Content); ok {
		for _, c := range parts {
			if c.File != nil && (len(c.File.Content) == 0 || !c.File.ImageDetail.Valid()) {
				return false
			}
		}
		return len(parts) > 0
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return data[0] == '"' || data[0] == '{' || data[0] == '[' || string(data) == "null"
}

// EvidenceMessages keeps asset placeholders associated with their image content.
func (input *DecisionInput) EvidenceMessages() []Message {
	messages := append([]Message(nil), input.Messages...)
	if len(messages) == 0 {
		data, _ := json.Marshal(input.State, json.Deterministic(true))
		messages = []Message{UserMessage(string(data))}
	}
	if len(input.Assets) > 0 {
		var content []Content
		for _, key := range slices.Sorted(maps.Keys(input.Assets)) {
			content = append(content, TextContent("Image asset replacing placeholder "+key+":"), FileContent(input.Assets[key]))
		}
		messages = append(messages, Message{Role: MessageRoleUser, Content: content})
	}
	return messages
}

func (b BoxCoordinates) Validate(confidence *float64) error {
	for _, v := range []float64{b.Top, b.Bottom, b.Left, b.Right} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return errors.New("box edges must be finite numbers between 0 and 1")
		}
	}
	if b.Top > b.Bottom || b.Left > b.Right {
		return errors.New("box edges are reversed")
	}
	if confidence != nil && (math.IsNaN(*confidence) || math.IsInf(*confidence, 0) || *confidence < 0 || *confidence > 1) {
		return errors.New("box confidence must be between 0 and 1")
	}
	return nil
}
