package decider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Decider = (*EmbedderAdapter)(nil)

type EmbedderAdapter struct {
	model    string
	embedder provider.Embedder
}

func FromEmbedder(model string, embedder provider.Embedder) *EmbedderAdapter {
	return &EmbedderAdapter{model: model, embedder: embedder}
}

func (a *EmbedderAdapter) Decide(ctx context.Context, input *provider.DecisionInput) (*provider.Decision, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	// Unwrap the HTTP string/array envelopes for semantic comparison.
	var stateValue any = input.State
	if len(input.State) == 1 {
		if text, ok := input.State["text"].(string); ok {
			stateValue = text
		} else if items, ok := input.State["items"].([]any); ok {
			stateValue = items
		}
	}
	state, err := embeddingText(stateValue)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(state) == "" {
		state = "(empty state)"
	}
	texts := []string{state}
	indices := map[string]int{state: 0}
	addText := func(text string) int {
		if index, ok := indices[text]; ok {
			return index
		}
		index := len(texts)
		texts = append(texts, text)
		indices[text] = index
		return index
	}
	// Each option has a standalone criterion and a question-aware criterion.
	// Combining their similarities keeps shared question wording from dominating
	// the answer description, while still taking the instructions into account.
	optionIndices := make([][][2]int, len(input.Questions))
	stateIndices := make([]int, len(input.Questions))
	for qi, q := range input.Questions {
		// Preserve the structured representation for ordered Score rubrics. The
		// level index is part of their meaning, unlike opaque Choice option IDs.
		if q.Score != nil {
			stateJSON, err := json.Marshal(input.State)
			if err != nil {
				return nil, err
			}
			stateIndices[qi] = addText(string(stateJSON))
			for _, option := range options(q) {
				text, err := json.Marshal(map[string]any{
					"question": q.Instructions, "answer": option.Label, "description": option.Description,
				})
				if err != nil {
					return nil, err
				}
				index := addText(string(text))
				optionIndices[qi] = append(optionIndices[qi], [2]int{index, index})
			}
			continue
		}
		question, err := embeddingText(q.Instructions)
		if err != nil {
			return nil, err
		}
		for _, option := range options(q) {
			description, err := embeddingText(option.Description)
			if err != nil {
				return nil, err
			}
			label := strings.ReplaceAll(option.Label, "_", " ")
			if q.Noul != nil {
				if option.Label == "true" {
					label = "Yes"
				} else {
					label = "No"
				}
			}
			if strings.TrimSpace(description) == "" {
				description = label
			}
			if strings.TrimSpace(description) == "" {
				description = "(unspecified answer)"
			}
			standalone := addText(description)
			contextual := standalone
			if question != "" {
				contextual = addText(question + "\n" + description)
			}
			optionIndices[qi] = append(optionIndices[qi], [2]int{standalone, contextual})
		}
	}

	embedding, err := a.embedder.Embed(ctx, texts, nil)
	if err != nil {
		return nil, err
	}
	if embedding == nil || len(embedding.Embeddings) != len(texts) {
		return nil, errors.New("embedding count does not match decision inputs")
	}
	dimensions := len(embedding.Embeddings[0])
	for _, vector := range embedding.Embeddings {
		if len(vector) != dimensions || dimensions == 0 {
			return nil, errors.New("decision embeddings must have matching nonzero dimensions")
		}
		var norm float64
		for _, value := range vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, errors.New("decision embeddings must contain finite values")
			}
			norm += float64(value) * float64(value)
		}
		if norm == 0 {
			return nil, errors.New("decision embeddings must not be zero vectors")
		}
	}

	result := &provider.Decision{Model: embedding.Model, Usage: embedding.Usage}
	if result.Model == "" {
		result.Model = a.model
	}
	for qi, q := range input.Questions {
		weights := make([]float64, len(options(q)))
		maximum := math.Inf(-1)
		for i := range weights {
			indices := optionIndices[qi][i]
			state := embedding.Embeddings[stateIndices[qi]]
			standalone := provider.CosineSimilarity(state, embedding.Embeddings[indices[0]])
			contextual := standalone
			if indices[0] != indices[1] {
				contextual = provider.CosineSimilarity(state, embedding.Embeddings[indices[1]])
			}
			weights[i] = (float64(standalone) + float64(contextual)) / 2
			maximum = max(maximum, weights[i])
		}
		// Softmax of cosine similarities gives a relative distribution. These
		// are similarity-based estimates, not generative model probabilities.
		for i := range weights {
			weights[i] = math.Exp(weights[i] - maximum)
		}
		value, err := answer(q, weights)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", q.ID, err)
		}
		result.Answers = append(result.Answers, value)
	}
	return result, nil
}

// embeddingText exposes the content of structured inputs as readable text while
// retaining field names, array order, and exact JSON numbers. Normalizing through
// JSON also supports typed structs/maps and custom marshalers accepted by Validate.
func embeddingText(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return "", err
	}
	return embeddingValue(normalized), nil
}

func embeddingValue(value any) string {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		var lines []string
		for _, key := range keys {
			text := embeddingValue(v[key])
			if strings.Contains(text, "\n") {
				lines = append(lines, key+":\n  "+strings.ReplaceAll(text, "\n", "\n  "))
			} else {
				lines = append(lines, key+": "+text)
			}
		}
		return strings.Join(lines, "\n")
	case []any:
		lines := make([]string, len(v))
		for i, item := range v {
			lines[i] = "- " + strings.ReplaceAll(embeddingValue(item), "\n", "\n  ")
		}
		return strings.Join(lines, "\n")
	case string:
		return v
	case nil:
		return "null"
	default:
		return fmt.Sprint(v)
	}
}
