package google

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// DecisionRequest is the Gemini Developer API's v1beta Decisions contract.
// Raw values preserve the string, single-block, and block-array input forms.
type DecisionRequest struct {
	Model     string                            `json:"model"`
	Input     jsontext.Value                    `json:"input"`
	Questions map[string]GeminiDecisionQuestion `json:"questions"`
}

type GeminiDecisionQuestion struct {
	Type         string         `json:"type"`
	Instructions jsontext.Value `json:"instructions"`
	Criteria     jsontext.Value `json:"criteria,omitzero"`
}

type decisionContent struct {
	Type       string  `json:"type"`
	Text       *string `json:"text,omitzero"`
	Data       string  `json:"data,omitzero"`
	URI        string  `json:"uri,omitzero"`
	MIMEType   string  `json:"mime_type,omitzero"`
	Resolution string  `json:"resolution,omitzero"`
}

// Keep the validated native input alongside portable Messages so Gemini can
// preserve URI references and all four resolution settings without fetching media.
type decisionContext jsontext.Value

func (c decisionContext) MarshalJSON() ([]byte, error) { return []byte(c), nil }

var decisionQuestionKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (r DecisionRequest) ProviderInput() (*provider.DecisionInput, error) {
	parts, err := parseDecisionContent(r.Input, false)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	if len(r.Questions) < 1 || len(r.Questions) > 1000 {
		return nil, errors.New("questions must contain 1 to 1000 questions")
	}
	input := &provider.DecisionInput{State: decisionContext(r.Input), Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: parts}}}
	for _, id := range slices.Sorted(maps.Keys(r.Questions)) {
		if !decisionQuestionKey.MatchString(id) {
			return nil, fmt.Errorf("invalid question key %q", id)
		}
		question := r.Questions[id]
		instructions, err := parseDecisionContent(question.Instructions, true)
		if err != nil {
			return nil, fmt.Errorf("questions[%q].instructions: %w", id, err)
		}
		var text strings.Builder
		for _, part := range instructions {
			text.WriteString(part.Text)
		}
		q := provider.DecisionQuestion{ID: id, Instructions: text.String()}
		switch question.Type {
		case "noul":
			q.Noul = &provider.NoulQuestion{}
			if len(question.Criteria) > 0 && string(question.Criteria) != "null" {
				var criteria map[string]string
				if decodeDecisionCriteria(question.Criteria, &criteria) != nil || criteria == nil {
					return nil, fmt.Errorf("questions[%q]: noul criteria must be an object of text descriptions", id)
				}
				for key, value := range criteria {
					switch key {
					case "true_description":
						q.Noul.True = value
					case "false_description":
						q.Noul.False = value
					default:
						return nil, fmt.Errorf("questions[%q]: unsupported noul criterion %q", id, key)
					}
				}
			}
		case "choice":
			var criteria map[string]string
			if decodeDecisionCriteria(question.Criteria, &criteria) != nil || len(criteria) < 2 || len(criteria) > 255 {
				return nil, fmt.Errorf("questions[%q]: choice requires 2 to 255 text criteria", id)
			}
			q.Choice = &provider.ChoiceQuestion{}
			for _, key := range slices.Sorted(maps.Keys(criteria)) {
				if !validDecisionOption(key) {
					return nil, fmt.Errorf("questions[%q]: invalid option key %q", id, key)
				}
				q.Choice.Options = append(q.Choice.Options, provider.DecisionOption{Label: key, Description: criteria[key]})
			}
		case "score":
			var criteria []string
			if json.Unmarshal(question.Criteria, &criteria) != nil || len(criteria) < 2 || len(criteria) > 255 {
				return nil, fmt.Errorf("questions[%q]: score requires 2 to 255 nonempty text criteria", id)
			}
			q.Score = &provider.ScoreQuestion{}
			for _, level := range criteria {
				if strings.TrimSpace(level) == "" {
					return nil, fmt.Errorf("questions[%q]: score levels must not be empty", id)
				}
				q.Score.Levels = append(q.Score.Levels, level)
			}
		default:
			return nil, fmt.Errorf("questions[%q]: unsupported type %q", id, question.Type)
		}
		input.Questions = append(input.Questions, q)
	}
	return input, input.Validate()
}

func decodeDecisionCriteria(raw jsontext.Value, target *map[string]string) error {
	var entries map[string]jsontext.Value
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	*target = make(map[string]string, len(entries))
	for key, value := range entries {
		if value.Kind() != '"' {
			return errors.New("criteria descriptions must be strings")
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return err
		}
		(*target)[key] = text
	}
	return nil
}

func validDecisionOption(key string) bool {
	if key == "" || len(key) > 512 || utf8.RuneCountInString(key) > 128 || strings.TrimSpace(key) != key {
		return false
	}
	for _, r := range key {
		if unicode.IsControl(r) || r != ' ' && unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func parseDecisionContent(raw jsontext.Value, textOnly bool) ([]provider.Content, error) {
	var text string
	if raw.Kind() == '"' && json.Unmarshal(raw, &text) == nil {
		if text == "" {
			return nil, errors.New("text must not be empty")
		}
		return []provider.Content{provider.TextContent(text)}, nil
	}
	var blocks []decisionContent
	switch raw.Kind() {
	case '{':
		var block decisionContent
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		blocks = []decisionContent{block}
	case '[':
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("expected text or content blocks")
	}
	var parts []provider.Content
	nonempty := false
	for _, block := range blocks {
		if block.Type == "text" {
			if block.Text == nil || block.Data != "" || block.URI != "" {
				return nil, errors.New("text block requires text")
			}
			nonempty = nonempty || *block.Text != ""
			parts = append(parts, provider.TextContent(*block.Text))
			continue
		}
		if textOnly {
			return nil, errors.New("question instructions support text only")
		}
		if err := block.validateMedia(); err != nil {
			return nil, err
		}
		content := []byte(block.URI)
		if block.Data != "" {
			content, _ = base64.StdEncoding.DecodeString(block.Data)
		}
		file := &provider.File{Content: content, ContentType: block.MIMEType}
		switch block.Resolution {
		case "low":
			file.ImageDetail = provider.ImageDetailLow
		case "high":
			file.ImageDetail = provider.ImageDetailHigh
		case "ultra_high":
			file.ImageDetail = provider.ImageDetailOriginal
		}
		parts = append(parts, provider.FileContent(file))
		nonempty = true
	}
	if !nonempty {
		return nil, errors.New("content must not be empty")
	}
	return parts, nil
}

func (b decisionContent) validateMedia() error {
	if b.Text != nil {
		return errors.New("media blocks cannot contain text")
	}
	var supported string
	switch b.Type {
	case "image":
		supported = "image/png image/jpeg image/webp image/heic image/heif image/gif image/bmp image/tiff"
	case "audio":
		supported = "audio/wav audio/mp3 audio/mpeg audio/aiff audio/aac audio/ogg audio/flac audio/m4a audio/opus audio/webm audio/l16 audio/alaw audio/mulaw"
	case "video":
		supported = "video/mp4 video/mpeg video/mov video/avi video/webm video/wmv video/x-flv video/3gpp"
	case "document":
		supported = "application/pdf"
	default:
		return fmt.Errorf("unsupported content type %q", b.Type)
	}
	if !slices.Contains(strings.Fields(supported), b.MIMEType) {
		return fmt.Errorf("unsupported %s MIME type %q", b.Type, b.MIMEType)
	}
	if (b.Data == "") == (b.URI == "") {
		return errors.New("media requires exactly one of data or uri")
	}
	if b.Data != "" {
		data, err := base64.StdEncoding.DecodeString(b.Data)
		if err != nil || len(data) == 0 {
			return errors.New("invalid base64 media data")
		}
	}
	if b.URI != "" {
		u, err := url.Parse(b.URI)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "youtu.be" || u.Hostname() == "youtube.com" || strings.HasSuffix(u.Hostname(), ".youtube.com") {
			return errors.New("media uri must be a Files API or HTTPS URL; YouTube is not supported")
		}
	}
	if b.Resolution != "" {
		if b.Type != "image" && b.Type != "video" {
			return errors.New("resolution is supported only for images and video")
		}
		if b.Resolution != "low" && b.Resolution != "medium" && b.Resolution != "high" && (b.Resolution != "ultra_high" || b.Type != "image") {
			return errors.New("invalid media resolution")
		}
	}
	return nil
}

type DecisionResponse struct {
	Answers map[string]GeminiDecisionAnswer `json:"answers"`
	Usage   DecisionUsage                   `json:"usage"`
}

type GeminiDecisionAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitzero"`
	Choice        *string            `json:"choice,omitzero"`
	Score         *float64           `json:"score,omitzero"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

type DecisionUsage struct {
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

func decisionProbability(p *float64) bool {
	return p != nil && !math.IsNaN(*p) && !math.IsInf(*p, 0) && *p >= 0 && *p <= 1
}

func (r DecisionResponse) decision(model string, input *provider.DecisionInput) (*provider.Decision, error) {
	if len(r.Answers) != len(input.Questions) {
		return nil, errors.New("Gemini decision response must answer every question exactly once")
	}
	result := &provider.Decision{Model: model, Usage: &provider.Usage{InputTokens: r.Usage.TotalInputTokens, OutputTokens: r.Usage.TotalOutputTokens}}
	for _, q := range input.Questions {
		a, ok := r.Answers[q.ID]
		if !ok || !decisionProbability(a.Confidence) {
			return nil, fmt.Errorf("question %q: missing answer or confidence", q.ID)
		}
		answer := provider.DecisionAnswer{ID: q.ID}
		switch {
		case q.Noul != nil:
			if a.Type != "noul" || !decisionProbability(a.Noul) {
				return nil, fmt.Errorf("question %q: invalid noul answer", q.ID)
			}
			answer.Noul = &provider.NoulAnswer{Probability: *a.Noul}
		case q.Choice != nil:
			if a.Type != "choice" || a.Choice == nil || len(a.Probabilities) != len(q.Choice.Options) {
				return nil, fmt.Errorf("question %q: invalid choice answer", q.ID)
			}
			if _, ok := a.Probabilities[value(a.Choice)]; !ok {
				return nil, errors.New("unknown selected decision choice")
			}
			for _, o := range q.Choice.Options {
				p, ok := a.Probabilities[o.Label]
				if !ok || !decisionProbability(&p) {
					return nil, errors.New("invalid decision choice probability")
				}
			}
			answer.Choice = &provider.ChoiceAnswer{Choice: *a.Choice, Confidence: *a.Confidence, Probabilities: a.Probabilities}
		case q.Score != nil:
			if a.Type != "score" || a.Score == nil || math.IsNaN(*a.Score) || math.IsInf(*a.Score, 0) || *a.Score < 0 || *a.Score > float64(len(q.Score.Levels)-1) || len(a.Probabilities) != len(q.Score.Levels) || len(a.Legend) != len(q.Score.Levels) {
				return nil, fmt.Errorf("question %q: invalid score answer", q.ID)
			}
			answer.Score = &provider.ScoreAnswer{Score: *a.Score, Confidence: *a.Confidence}
			customScore := 0.0
			for i := range q.Score.Levels {
				key := strconv.Itoa(i)
				p, ok := a.Probabilities[key]
				description, exists := a.Legend[key]
				if !ok || !exists || !decisionProbability(&p) {
					return nil, errors.New("invalid decision score level")
				}
				val, label := i, ""
				if len(q.Score.Values) > i {
					val = q.Score.Values[i]
				}
				if len(q.Score.Labels) > i {
					label = q.Score.Labels[i]
				}
				answer.Score.Levels = append(answer.Score.Levels, provider.ScoreLevel{Value: val, Label: label, Description: description, Probability: p})
				customScore += float64(val) * p
			}
			if len(q.Score.Values) > 0 {
				answer.Score.Score = customScore
			}
		default:
			return nil, errors.New("unsupported Gemini decision answer")
		}
		result.Answers = append(result.Answers, answer)
	}
	return result, nil
}

// DecisionResponseFromProvider translates answers from any configured decider.
func DecisionResponseFromProvider(result *provider.Decision) (*DecisionResponse, error) {
	if result == nil {
		return nil, errors.New("empty decision response")
	}
	r := &DecisionResponse{Answers: make(map[string]GeminiDecisionAnswer, len(result.Answers))}
	if result.Usage != nil {
		r.Usage.TotalInputTokens, r.Usage.TotalOutputTokens = result.Usage.InputTokens, result.Usage.OutputTokens
		r.Usage.TotalTokens = result.Usage.InputTokens + result.Usage.OutputTokens
	}
	for _, answer := range result.Answers {
		var a GeminiDecisionAnswer
		switch {
		case answer.Noul != nil:
			a.Type, a.Noul = "noul", &answer.Noul.Probability
			// Gemini defines binary confidence as the more likely outcome.
			a.Confidence = new(math.Max(*a.Noul, 1-*a.Noul))
		case answer.Choice != nil:
			a.Type, a.Choice, a.Confidence, a.Probabilities = "choice", &answer.Choice.Choice, &answer.Choice.Confidence, answer.Choice.Probabilities
		case answer.Score != nil:
			a.Type, a.Score, a.Confidence = "score", &answer.Score.Score, &answer.Score.Confidence
			a.Probabilities, a.Legend = make(map[string]float64), make(map[string]string)
			for i, level := range answer.Score.Levels {
				key := strconv.Itoa(i)
				a.Probabilities[key] = level.Probability
				text, err := decisionText(level.Description)
				if err != nil {
					return nil, err
				}
				a.Legend[key] = text
			}
		default:
			return nil, errors.New("Gemini decisions cannot represent box, refusal, or error answers")
		}
		r.Answers[answer.ID] = a
	}
	return r, nil
}
