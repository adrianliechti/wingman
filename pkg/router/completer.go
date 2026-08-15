package router

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strconv"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router/internal/executor"
	"github.com/adrianliechti/wingman/pkg/router/internal/routingtelemetry"
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

	name         string
	kind         string
	candidateIDs []string
	fallbackID   string
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

// WithRoutingMetadata assigns stable, configured identifiers to routing
// telemetry. IDs must correspond to the completers slice.
func WithRoutingMetadata(name, kind string, candidateIDs []string, fallbackID string) Option {
	return func(c *Completer) {
		c.name = name
		c.kind = kind
		c.candidateIDs = append([]string(nil), candidateIDs...)
		c.fallbackID = fallbackID
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
	if c.strategy == nil {
		return nil, errors.New("router strategy is required")
	}
	for _, completer := range c.completers {
		if completer == nil {
			return nil, errors.New("router completers must not be nil")
		}
	}
	if len(c.candidateIDs) > 0 && len(c.candidateIDs) != len(c.completers) {
		return nil, errors.New("router candidate ids must match completers")
	}
	if c.name == "" {
		c.name = "router"
	}
	if c.kind == "" {
		c.kind = "health"
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
		var previousID string
		var previousFailure executor.FailureKind
		decisionRecorded := false

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

			candidateID := c.candidateID(index)
			if !decisionRecorded {
				routingtelemetry.RecordDecision(ctx, routingtelemetry.Decision{
					Router:     c.name,
					Kind:       c.kind,
					Model:      candidateID,
					Source:     "strategy",
					Candidates: len(c.completers),
				})
				decisionRecorded = true
			} else {
				routingtelemetry.RecordFallback(ctx, routingtelemetry.Fallback{
					Router: c.name,
					Kind:   c.kind,
					From:   previousID,
					To:     candidateID,
					Reason: string(previousFailure),
				})
			}

			done, err, failure := c.attempt(ctx, index, probe, messages, options, yield)

			if done {
				return
			}

			if err != nil {
				lastErr = err
			}
			previousID = candidateID
			previousFailure = failure
		}

		if c.fallback != nil {
			fallbackID := c.fallbackID
			if fallbackID == "" {
				fallbackID = "fallback"
			}
			if !decisionRecorded {
				routingtelemetry.RecordDecision(ctx, routingtelemetry.Decision{
					Router: c.name, Kind: c.kind, Model: fallbackID, Source: "fallback", Candidates: 1,
				})
			} else if previousID != "" {
				routingtelemetry.RecordFallback(ctx, routingtelemetry.Fallback{
					Router: c.name, Kind: c.kind, From: previousID, To: fallbackID, Reason: string(previousFailure),
				})
			}

			candidates := []executor.Candidate{{ID: fallbackID, Completer: c.fallback}}
			hooks := executor.Hooks{Attempt: func(candidate executor.Candidate, result executor.Result) {
				routingtelemetry.RecordAttempt(ctx, routingtelemetry.Attempt{
					Router: c.name, Kind: c.kind, Model: candidate.ID, Failure: string(result.Failure), Delivered: result.Delivered, TTFT: result.TTFT,
				})
			}}

			for completion, err := range executor.Complete(ctx, candidates, messages, options, c.firstTokenTimeout, hooks) {
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
func (c *Completer) attempt(ctx context.Context, index int, probe bool, messages []provider.Message, options *provider.CompleteOptions, yield func(*provider.Completion, error) bool) (bool, error, executor.FailureKind) {
	stat := c.stats[index]
	result := executor.Attempt(ctx, c.completers[index], messages, options, c.firstTokenTimeout, yield)
	routingtelemetry.RecordAttempt(ctx, routingtelemetry.Attempt{
		Router:    c.name,
		Kind:      c.kind,
		Model:     c.candidateID(index),
		Failure:   string(result.Failure),
		Delivered: result.Delivered,
		TTFT:      result.TTFT,
	})

	switch {
	case result.Delivered && result.Failure == executor.FailureProvider:
		// A stream that terminated with a provider error counts against
		// health even though the partial output went to the caller
		stat.RecordFailure(c.failureThreshold, probe, result.Err)
		return true, nil, result.Failure

	case result.Delivered && result.Failure == executor.FailureCaller:
		stat.Release(probe)
		return true, nil, result.Failure

	case result.Delivered:
		stat.RecordSuccess(result.TTFT, probe)
		return true, nil, result.Failure

	case result.Failure == executor.FailureCaller:
		// The caller went away - this says nothing about provider health
		stat.Release(probe)
		yield(nil, result.Err)
		return true, nil, result.Failure

	case result.Failure == executor.FailureRequest:
		// Errors caused by the request itself (invalid request, context too
		// long) would fail on every provider: surface them directly and
		// leave the health alone
		stat.Release(probe)
		yield(nil, result.Err)
		return true, nil, result.Failure

	default:
		stat.RecordFailure(c.failureThreshold, probe, result.Err)
		return false, result.Err, result.Failure
	}
}

func (c *Completer) candidateID(index int) string {
	if len(c.candidateIDs) == len(c.completers) && c.candidateIDs[index] != "" {
		return c.candidateIDs[index]
	}
	return "candidate:" + strconv.Itoa(index)
}
