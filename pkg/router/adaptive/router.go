package adaptive

import (
	"context"
	"errors"
	"iter"
	"math/rand"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
)

const (
	defaultLatencyAlpha = 0.3 // Exponential moving average weight
)

// Completer implements a smart router with circuit breaker and TTFT-based selection
type Completer struct {
	completers []provider.Completer
	stats      []*router.ProviderStats

	failureThreshold int
	recoveryTimeout  time.Duration
	latencyAlpha     float64
}

// NewCompleter creates a new smart router with sensible defaults
func NewCompleter(completers ...provider.Completer) (provider.Completer, error) {
	if len(completers) == 0 {
		return nil, errors.New("at least one completer is required")
	}

	stats := make([]*router.ProviderStats, len(completers))
	for i := range stats {
		stats[i] = router.NewProviderStats()
	}

	return &Completer{
		completers:       completers,
		stats:            stats,
		failureThreshold: router.DefaultFailureThreshold,
		recoveryTimeout:  router.DefaultRecoveryTimeout,
		latencyAlpha:     defaultLatencyAlpha,
	}, nil
}

// Complete routes the request to the best available provider
func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		index := c.selectProvider()

		if index < 0 {
			yield(nil, errors.New("all providers are unavailable"))
			return
		}

		// Track inflight request (atomic, lock-free)
		c.stats[index].AddInflight(1)
		defer c.stats[index].AddInflight(-1)

		start := time.Now()

		var ttft time.Duration // Time to first token
		var hasResponse bool

		for completion, err := range c.completers[index].Complete(ctx, messages, options) {
			if err != nil {
				if !yield(completion, err) {
					break
				}

				continue
			}

			// Record TTFT on first successful response chunk
			if !hasResponse {
				ttft = time.Since(start)
			}

			hasResponse = true

			if !yield(completion, nil) {
				break
			}
		}

		// Update stats after completion
		if hasResponse {
			c.stats[index].RecordSuccess(ttft, c.latencyAlpha)
		} else {
			// No response received - either error or empty iterator (both are failures)
			c.stats[index].RecordFailure(c.failureThreshold)
		}
	}
}

// selectProvider chooses the best available provider using weighted random selection
// Scoring considers: TTFT (responsiveness), error rate, and current load (inflight requests)
func (c *Completer) selectProvider() int {
	candidates := make([]int, 0, len(c.completers))
	scores := make([]float64, 0, len(c.completers))

	for i, stat := range c.stats {
		if !stat.IsAvailable(c.recoveryTimeout) {
			continue
		}

		state, avgTTFT, totalRequests, totalFailures, inflight := stat.GetMetrics()

		candidates = append(candidates, i)

		// Calculate score: higher is better
		// Base score inversely proportional to TTFT (time to first token)
		ttftMs := float64(avgTTFT.Milliseconds())

		if ttftMs < 1 {
			ttftMs = 1
		}

		// Error rate factor (0 to 1)
		var errorRate float64

		if totalRequests > 0 {
			errorRate = float64(totalFailures) / float64(totalRequests)
		}

		// Inflight penalty: reduces score as concurrent requests increase
		// This helps distribute load evenly and respect per-provider quotas
		// Formula: 1 / (1 + inflight) gives diminishing returns as load increases
		inflightFactor := 1.0 / (1.0 + float64(inflight))

		// Score formula: prefer lower TTFT, lower error rate, and lower inflight count
		// score = inflightFactor / (ttft * (1 + errorRate * 10))
		score := inflightFactor / (ttftMs * (1 + errorRate*10))

		// Penalize half-open circuits to limit probe traffic
		if state == router.CircuitHalfOpen {
			score *= 0.1
		}

		scores = append(scores, score)
	}

	if len(candidates) == 0 {
		// All circuits are open - fallback to least recently failed
		return c.fallbackProvider()
	}

	// Weighted random selection based on scores
	return c.weightedSelect(candidates, scores)
}

// weightedSelect performs weighted random selection based on scores
func (c *Completer) weightedSelect(candidates []int, scores []float64) int {
	if len(candidates) == 1 {
		return candidates[0]
	}

	// Calculate total score
	var totalScore float64

	for _, score := range scores {
		totalScore += score
	}

	if totalScore <= 0 {
		// All scores are zero, pick randomly
		return candidates[rand.Intn(len(candidates))]
	}

	// Pick a random point in the total score range
	r := rand.Float64() * totalScore

	var cumulative float64

	for i, score := range scores {
		cumulative += score

		if r <= cumulative {
			return candidates[i]
		}
	}

	// Fallback to last candidate (shouldn't happen)
	return candidates[len(candidates)-1]
}

// fallbackProvider returns the least recently failed provider when all circuits are open
func (c *Completer) fallbackProvider() int {
	bestIndex := 0

	var oldestFailure time.Time

	for i, stat := range c.stats {
		lastFailure := stat.GetLastFailure()

		if i == 0 || lastFailure.Before(oldestFailure) {
			oldestFailure = lastFailure
			bestIndex = i
		}
	}

	// Transition to half-open for the fallback
	c.stats[bestIndex].SetHalfOpen()

	return bestIndex
}
