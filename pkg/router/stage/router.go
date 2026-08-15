// Package stage implements an opt-in, signal-driven router for coding-agent
// turns. It spends the capable model on exploration and error recovery, and
// uses the efficient model for settled production work.
package stage

import (
	"context"
	"errors"
	"iter"
	"math"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router/internal/executor"
	"github.com/adrianliechti/wingman/pkg/router/internal/routingtelemetry"
)

type Picker string

const (
	EfficientFirst Picker = "efficient_first"
	CapableFirst   Picker = "capable_first"
)

type Tier string

const (
	TierEfficient Tier = "efficient"
	TierCapable   Tier = "capable"
)

type Candidate struct {
	Model     string
	Completer provider.Completer
}

type Options struct {
	Name                string
	Picker              Picker
	ConfidenceThreshold float64
	RecentWindow        int
	FirstTokenTimeout   time.Duration
}

type Decision struct {
	Tier       Tier    `json:"tier"`
	Model      string  `json:"model"`
	Source     string  `json:"source"`
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
	Signals    Signals `json:"signals"`
}

type Completer struct {
	capable   Candidate
	efficient Candidate
	options   Options
}

var _ provider.Completer = (*Completer)(nil)

func NewCompleter(capable, efficient Candidate, options Options) (*Completer, error) {
	if capable.Completer == nil || efficient.Completer == nil {
		return nil, errors.New("stage router candidates require completers")
	}
	if capable.Model == "" || efficient.Model == "" {
		return nil, errors.New("stage router candidates require models")
	}
	if capable.Model == efficient.Model {
		return nil, errors.New("stage router candidates must be different")
	}
	if options.Picker == "" {
		options.Picker = EfficientFirst
	}
	if options.Picker != EfficientFirst && options.Picker != CapableFirst {
		return nil, errors.New("stage router picker must be efficient_first or capable_first")
	}
	if math.IsNaN(options.ConfidenceThreshold) || math.IsInf(options.ConfidenceThreshold, 0) || options.ConfidenceThreshold < 0 || options.ConfidenceThreshold > 1 {
		return nil, errors.New("stage router confidence threshold must be between 0 and 1")
	}
	if options.RecentWindow < 0 {
		return nil, errors.New("stage router recent window must not be negative")
	}
	if options.RecentWindow == 0 {
		options.RecentWindow = 3
	}
	if options.FirstTokenTimeout < 0 {
		return nil, errors.New("stage router first token timeout must not be negative")
	}
	if options.Name == "" {
		options.Name = "stage"
	}

	return &Completer{capable: capable, efficient: efficient, options: options}, nil
}

// Route explains the selected tier without invoking either candidate.
func (c *Completer) Route(messages []provider.Message) Decision {
	signals := extractSignals(messages, c.options.RecentWindow)
	decision := Decision{Signals: signals}

	selectTier := func(tier Tier, source string, score, confidence float64) Decision {
		decision.Tier = tier
		decision.Source = source
		decision.Score = score
		decision.Confidence = confidence
		if tier == TierCapable {
			decision.Model = c.capable.Model
		} else {
			decision.Model = c.efficient.Model
		}
		return decision
	}

	if signals.Compacted || signals.Severity >= 1 {
		return selectTier(TierCapable, "override", 1, 1)
	}
	if signals.TestsPassed && signals.RecentWrites+signals.RecentEdits > 0 && signals.Severity == 0 {
		return selectTier(TierEfficient, "tests_passed", -1, 1)
	}

	defaultTier := TierEfficient
	if c.options.Picker == CapableFirst {
		defaultTier = TierCapable
	}

	if signals.RecentToolResults == 0 && signals.RecentWrites+signals.RecentEdits+signals.RecentReads+signals.RecentPlans+signals.RecentOther == 0 {
		return selectTier(defaultTier, "no_signal", 0, 0)
	}

	wrong := signals.Severity / 0.7
	if signals.Spinning {
		wrong++
	}
	if signals.Exploring {
		wrong++
	}
	score := math.Tanh(0.5 * (wrong - signals.Production))
	confidence := math.Abs(score)

	if confidence >= c.options.ConfidenceThreshold && score != 0 {
		if score > 0 {
			return selectTier(TierCapable, "dimensions", score, confidence)
		}
		return selectTier(TierEfficient, "dimensions", score, confidence)
	}

	return selectTier(defaultTier, "default", score, confidence)
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	decision := c.Route(messages)
	selected, fallback := c.efficient, c.capable
	if decision.Tier == TierCapable {
		selected, fallback = c.capable, c.efficient
	}

	routingtelemetry.RecordDecision(ctx, routingtelemetry.Decision{
		Router: c.options.Name, Kind: "stage", Model: selected.Model,
		Source: decision.Source, Score: decision.Score, Candidates: 2,
	})

	candidates := []executor.Candidate{
		{ID: selected.Model, Completer: selected.Completer},
		{ID: fallback.Model, Completer: fallback.Completer},
	}
	hooks := executor.Hooks{
		Attempt: func(candidate executor.Candidate, result executor.Result) {
			routingtelemetry.RecordAttempt(ctx, routingtelemetry.Attempt{
				Router: c.options.Name, Kind: "stage", Model: candidate.ID,
				Failure: string(result.Failure), Delivered: result.Delivered, TTFT: result.TTFT,
			})
		},
		Fallback: func(from, to executor.Candidate, result executor.Result) {
			routingtelemetry.RecordFallback(ctx, routingtelemetry.Fallback{
				Router: c.options.Name, Kind: "stage", From: from.ID, To: to.ID, Reason: string(result.Failure),
			})
		},
	}

	return executor.Complete(ctx, candidates, messages, options, c.options.FirstTokenTimeout, hooks)
}
