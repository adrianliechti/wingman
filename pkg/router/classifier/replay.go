package classifier

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// RouteDecision is the explainable, provider-free result of classification.
// Candidates is the exact execution order, including eligible fallbacks.
type RouteDecision struct {
	Model      string   `json:"model"`
	Candidates []string `json:"candidates"`
	Source     string   `json:"source"`
	Score      float64  `json:"score"`
	Cached     bool     `json:"cached"`
}

// Route classifies a request without invoking the selected provider. It uses
// the live decision cache and optional embedding/judge tiers just like Complete.
func (c *Completer) Route(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) RouteDecision {
	return c.routeDecision(c.classify(ctx, messages, options))
}

func (c *Completer) routeDecision(d decision) RouteDecision {
	result := RouteDecision{
		Source: d.source,
		Score:  d.score,
		Cached: d.cached,
	}

	for _, index := range d.candidates {
		result.Candidates = append(result.Candidates, c.candidates[index].Model)
	}
	if len(result.Candidates) > 0 {
		result.Model = result.Candidates[0]
	}

	return result
}

// ReplayCase is one labeled or unlabeled offline routing example.
type ReplayCase struct {
	ID            string                    `json:"id,omitempty"`
	Messages      []provider.Message        `json:"messages"`
	Options       *provider.CompleteOptions `json:"options,omitempty"`
	ExpectedModel string                    `json:"expected_model,omitempty"`
}

// ReplayResult explains one independent routing prediction.
type ReplayResult struct {
	ID            string        `json:"id,omitempty"`
	ExpectedModel string        `json:"expected_model,omitempty"`
	Decision      RouteDecision `json:"decision"`
	Correct       *bool         `json:"correct,omitempty"`
	CostDelta     *float64      `json:"cost_delta,omitempty"`
}

// ReplayReport summarizes model-label accuracy and cost/capability drift. A
// positive MeanCostDelta means predictions cost more than the labeled model.
type ReplayReport struct {
	Total         int                       `json:"total"`
	Labeled       int                       `json:"labeled"`
	Correct       int                       `json:"correct"`
	Accuracy      float64                   `json:"accuracy"`
	UnderRouted   int                       `json:"under_routed"`
	OverRouted    int                       `json:"over_routed"`
	MeanCostDelta float64                   `json:"mean_cost_delta"`
	Predicted     map[string]int            `json:"predicted"`
	Sources       map[string]int            `json:"sources"`
	Confusion     map[string]map[string]int `json:"confusion"`
	Results       []ReplayResult            `json:"results,omitempty"`
}

// Replay classifies cases independently, bypassing affinity/cache state so
// duplicate examples remain separate observations. It never invokes a routed
// candidate; optional classifier embedder/judge tiers may still run.
func (c *Completer) Replay(ctx context.Context, cases []ReplayCase) ReplayReport {
	report := ReplayReport{
		Total:     len(cases),
		Predicted: make(map[string]int),
		Sources:   make(map[string]int),
		Confusion: make(map[string]map[string]int),
		Results:   make([]ReplayResult, 0, len(cases)),
	}

	modelIndex := make(map[string]int, len(c.candidates))
	for i, candidate := range c.candidates {
		modelIndex[candidate.Model] = i
	}

	var costDelta float64

	for _, replayCase := range cases {
		d := c.decide(ctx, extractSignals(replayCase.Messages, replayCase.Options))
		prediction := c.routeDecision(d)
		result := ReplayResult{ID: replayCase.ID, ExpectedModel: replayCase.ExpectedModel, Decision: prediction}

		report.Predicted[prediction.Model]++
		report.Sources[prediction.Source]++

		if expectedIndex, ok := modelIndex[replayCase.ExpectedModel]; ok {
			report.Labeled++
			correct := replayCase.ExpectedModel == prediction.Model
			result.Correct = &correct
			if correct {
				report.Correct++
			}

			if report.Confusion[replayCase.ExpectedModel] == nil {
				report.Confusion[replayCase.ExpectedModel] = make(map[string]int)
			}
			report.Confusion[replayCase.ExpectedModel][prediction.Model]++

			predictedIndex := modelIndex[prediction.Model]
			delta := c.candidates[predictedIndex].Cost - c.candidates[expectedIndex].Cost
			result.CostDelta = &delta
			costDelta += delta

			switch {
			case c.candidates[predictedIndex].MaxDifficulty < c.candidates[expectedIndex].MaxDifficulty:
				report.UnderRouted++
			case c.candidates[predictedIndex].MaxDifficulty > c.candidates[expectedIndex].MaxDifficulty:
				report.OverRouted++
			}
		}

		report.Results = append(report.Results, result)
	}

	if report.Labeled > 0 {
		report.Accuracy = float64(report.Correct) / float64(report.Labeled)
		report.MeanCostDelta = costDelta / float64(report.Labeled)
	}

	return report
}
