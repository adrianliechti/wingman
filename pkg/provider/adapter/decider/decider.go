package decider

import (
	"errors"
	"math"
	"strconv"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func options(q provider.DecisionQuestion) []provider.DecisionOption {
	switch {
	case q.Noul != nil:
		return []provider.DecisionOption{
			{Label: "true", Description: q.Noul.True},
			{Label: "false", Description: q.Noul.False},
		}
	case q.Choice != nil:
		return q.Choice.Options
	default:
		result := make([]provider.DecisionOption, len(q.Score.Levels))
		for i, description := range q.Score.Levels {
			result[i] = provider.DecisionOption{Label: strconv.Itoa(i), Description: description}
		}
		return result
	}
}

// answer derives all output fields from one normalized distribution. Confidence
// is one minus normalized Shannon entropy, not a calibrated correctness score.
func answer(q provider.DecisionQuestion, weights []float64) (provider.DecisionAnswer, error) {
	choices := options(q)
	if len(weights) != len(choices) {
		return provider.DecisionAnswer{}, errors.New("probability count does not match criteria")
	}
	var total float64
	for _, value := range weights {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return provider.DecisionAnswer{}, errors.New("probabilities must be finite numbers between 0 and 1")
		}
		total += value
	}
	if total <= 0 {
		return provider.DecisionAnswer{}, errors.New("probabilities must have positive total mass")
	}
	probabilities := make([]float64, len(weights))
	var entropy float64
	best := 0
	for i, weight := range weights {
		p := weight / total
		probabilities[i] = p
		if p > 0 {
			entropy -= p * math.Log(p)
		}
		if p > probabilities[best] {
			best = i
		}
	}
	confidence := 1.0
	if len(weights) > 1 {
		confidence = max(0, min(1, 1-entropy/math.Log(float64(len(weights)))))
	}

	result := provider.DecisionAnswer{ID: q.ID}
	switch {
	case q.Noul != nil:
		result.Noul = &provider.NoulAnswer{Probability: probabilities[0]}
	case q.Choice != nil:
		distribution := make(map[string]float64, len(choices))
		for i, option := range choices {
			distribution[option.Label] = probabilities[i]
		}
		result.Choice = &provider.ChoiceAnswer{
			Choice: choices[best].Label, Probabilities: distribution, Confidence: confidence,
		}
	case q.Score != nil:
		result.Score = &provider.ScoreAnswer{Confidence: confidence, Levels: make([]provider.ScoreLevel, len(choices))}
		for i, option := range choices {
			result.Score.Score += float64(i) * probabilities[i]
			result.Score.Levels[i] = provider.ScoreLevel{Description: option.Description, Probability: probabilities[i]}
		}
	}
	return result, nil
}
