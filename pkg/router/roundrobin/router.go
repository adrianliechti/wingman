package roundrobin

import (
	"context"
	"errors"
	"iter"
	"math/rand"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
)

// Completer implements a simple round-robin router with circuit breaker protection.
// Unlike the adaptive router, this does not track TTFT or inflight requests -
// it simply distributes load randomly among healthy providers.
type Completer struct {
	completers []provider.Completer
	stats      []*router.ProviderStats

	failureThreshold int
	recoveryTimeout  time.Duration
}

// NewCompleter creates a new round-robin router with circuit breaker protection
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
	}, nil
}

// Complete routes the request to a randomly selected healthy provider
func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		index := c.selectProvider()

		if index < 0 {
			yield(nil, errors.New("all providers are unavailable"))
			return
		}

		var hasResponse bool

		for completion, err := range c.completers[index].Complete(ctx, messages, options) {
			if err != nil {
				if !yield(completion, err) {
					break
				}

				continue
			}

			hasResponse = true

			if !yield(completion, nil) {
				break
			}
		}

		// Update circuit breaker state only
		if hasResponse {
			c.stats[index].RecordSuccess(0, 0) // No TTFT tracking
		} else {
			c.stats[index].RecordFailure(c.failureThreshold)
		}
	}
}

// selectProvider randomly selects from available (healthy) providers
func (c *Completer) selectProvider() int {
	candidates := make([]int, 0, len(c.completers))

	for i, stat := range c.stats {
		if stat.IsAvailable(c.recoveryTimeout) {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		// All circuits are open - fallback to least recently failed
		return c.fallbackProvider()
	}

	// Random selection among healthy providers
	return candidates[rand.Intn(len(candidates))]
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
