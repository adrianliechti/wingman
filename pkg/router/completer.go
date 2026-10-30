package router

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Strategy selects the next provider index from the given candidates.
// candidates is never empty; stats is indexed by provider, not by candidate.
type Strategy func(candidates []int, stats []*ProviderStats) int

// Completer routes requests across multiple providers with circuit breaker
// protection, a first-token deadline and transparent failover: if a provider
// fails before producing any output, the request is retried on the next
// healthy provider instead of surfacing the error to the caller.
type Completer struct {
	completers []provider.Completer
	stats      []*ProviderStats
	strategy   Strategy

	fallback provider.Completer

	failureThreshold  int
	recoveryTimeout   time.Duration
	firstTokenTimeout time.Duration
}

type Option func(*Completer)

// WithFallback sets a fallback completer used when all primary providers are unavailable
func WithFallback(fallback provider.Completer) Option {
	return func(c *Completer) {
		c.fallback = fallback
	}
}

// WithFirstTokenTimeout bounds the wait for the first response token. A
// provider that produces nothing within this window is recorded as failed and
// the request fails over to the next provider. Zero disables the deadline.
func WithFirstTokenTimeout(timeout time.Duration) Option {
	return func(c *Completer) {
		c.firstTokenTimeout = timeout
	}
}

// WithFailureThreshold sets the number of consecutive failures that open a circuit
func WithFailureThreshold(threshold int) Option {
	return func(c *Completer) {
		c.failureThreshold = threshold
	}
}

// WithRecoveryTimeout sets how long an open circuit waits before allowing a probe
func WithRecoveryTimeout(timeout time.Duration) Option {
	return func(c *Completer) {
		c.recoveryTimeout = timeout
	}
}

// NewCompleter creates a router that picks providers using the given strategy
func NewCompleter(completers []provider.Completer, strategy Strategy, options ...Option) (*Completer, error) {
	if len(completers) == 0 {
		return nil, errors.New("at least one completer is required")
	}

	stats := make([]*ProviderStats, len(completers))
	for i := range stats {
		stats[i] = NewProviderStats()
	}

	c := &Completer{
		completers: completers,
		stats:      stats,
		strategy:   strategy,

		failureThreshold:  DefaultFailureThreshold,
		recoveryTimeout:   DefaultRecoveryTimeout,
		firstTokenTimeout: DefaultFirstTokenTimeout,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

// Stats exposes the per-provider stats, indexed like the completers slice
func (c *Completer) Stats() []*ProviderStats {
	return c.stats
}

// Complete routes the request to the best available provider, failing over to
// other providers as long as no output has been delivered to the caller
func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	messages = ScrubMessages(messages)
	options = ScrubOptions(options)

	return func(yield func(*provider.Completion, error) bool) {
		tried := make(map[int]bool, len(c.completers))

		var lastErr error

		for len(tried) < len(c.completers) {
			if ctx.Err() != nil {
				yield(nil, ctx.Err())
				return
			}

			index, probe := c.acquire(tried)

			if index < 0 {
				break
			}

			tried[index] = true

			done, err := c.attempt(ctx, index, probe, messages, options, yield)

			if done {
				return
			}

			if err != nil {
				lastErr = err
			}
		}

		if c.fallback != nil {
			for completion, err := range c.fallback.Complete(ctx, messages, options) {
				if !yield(completion, err) {
					return
				}
			}

			return
		}

		if lastErr != nil {
			yield(nil, lastErr)
			return
		}

		yield(nil, &provider.ProviderError{
			Code:    http.StatusServiceUnavailable,
			Message: "all providers are unavailable",
		})
	}
}

// acquire selects and claims the next provider to try. Providers in `tried`
// are excluded; losing an acquire race marks the provider as tried so the
// request moves on instead of spinning on it.
func (c *Completer) acquire(tried map[int]bool) (index int, probe bool) {
	for {
		candidates := make([]int, 0, len(c.completers))

		for i, stat := range c.stats {
			if tried[i] || !stat.IsCandidate(c.recoveryTimeout) {
				continue
			}

			candidates = append(candidates, i)
		}

		if len(candidates) == 0 {
			return -1, false
		}

		index := c.strategy(candidates, c.stats)

		if index < 0 {
			return -1, false
		}

		if acquired, probe := c.stats[index].Acquire(c.recoveryTimeout); acquired {
			return index, probe
		}

		tried[index] = true
	}
}

// attempt runs the request against a single provider. It returns done=true
// when the request finished from the caller's perspective (output delivered,
// caller gone, non-retryable error) and the router must not fail over.
// Otherwise the returned error describes why the attempt failed before
// producing output.
func (c *Completer) attempt(ctx context.Context, index int, probe bool, messages []provider.Message, options *provider.CompleteOptions, yield func(*provider.Completion, error) bool) (bool, error) {
	stat := c.stats[index]

	attemptCtx := ctx

	var timer *time.Timer

	if c.firstTokenTimeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithCancel(ctx)
		defer cancel()

		timer = time.AfterFunc(c.firstTokenTimeout, cancel)
		defer timer.Stop()
	}

	start := time.Now()

	var ttft time.Duration
	var delivered bool
	var attemptErr, streamErr error

	for completion, err := range c.completers[index].Complete(attemptCtx, messages, options) {
		if err != nil {
			// Before any output the error stays internal so the request can
			// fail over; afterwards it must be passed through to the caller
			if !delivered {
				attemptErr = err
				break
			}

			streamErr = err

			if !yield(completion, err) {
				break
			}

			continue
		}

		if delivered {
			streamErr = nil
		} else {
			delivered = true
			ttft = time.Since(start)

			if timer != nil {
				timer.Stop()
			}
		}

		if !yield(completion, nil) {
			break
		}
	}

	switch {
	case delivered:
		// A stream that terminated with a provider error counts against
		// health even though the partial output went to the caller
		if streamErr != nil && ctx.Err() == nil && attemptCtx.Err() == nil {
			stat.RecordFailure(c.failureThreshold, probe, streamErr)
		} else {
			stat.RecordSuccess(ttft, probe)
		}

		return true, nil

	case ctx.Err() != nil:
		// The caller went away - this says nothing about provider health
		stat.Release(probe)
		yield(nil, ctx.Err())
		return true, nil

	case attemptErr != nil:
		// The first-token timer is the only other cancellation source
		if attemptCtx.Err() != nil {
			stat.RecordFailure(c.failureThreshold, probe, nil)
			return false, &provider.ProviderError{
				Code:    http.StatusGatewayTimeout,
				Message: fmt.Sprintf("no response within %s", c.firstTokenTimeout),
				Err:     attemptErr,
			}
		}

		// Errors caused by the request itself (invalid request, context too
		// long) would fail on every provider: surface them directly and
		// leave the health alone
		if isRequestError(attemptErr) {
			stat.Release(probe)
			yield(nil, attemptErr)
			return true, nil
		}

		stat.RecordFailure(c.failureThreshold, probe, attemptErr)
		return false, attemptErr

	default:
		stat.RecordFailure(c.failureThreshold, probe, nil)
		return false, errors.New("provider returned no response")
	}
}

// isRequestError reports whether the error reflects the request itself rather
// than the provider, so failing over could not help. Auth (401/403), not
// found (404), timeout (408) and rate limit (429) responses are excluded:
// keys, deployments and quotas are per-provider configuration, so those must
// fail over and count against the failing provider's health.
func isRequestError(err error) bool {
	if errors.Is(err, provider.ErrRejected) {
		return true
	}

	code := provider.CodeFromError(err, 0)

	if code < 400 || code >= 500 {
		return false
	}

	switch code {
	case http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusRequestTimeout,
		http.StatusTooManyRequests:
		return false
	}

	return true
}
