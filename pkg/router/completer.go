package router

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"slices"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Strategy selects the next provider index from the given candidates.
// candidates is never empty; stats is indexed by provider, not by candidate.
type Strategy func(candidates []int, stats []*ProviderStats) int

// FirstPick selects a healthy candidate for the first attempt of a request.
// Retries use the router's ordinary strategy.
type FirstPick func(context.Context, []provider.Message, *provider.CompleteOptions, []int) (int, error)

// Completer routes requests across multiple providers with circuit breaker
// protection, a first-token deadline and transparent failover: if a provider
// fails before producing any output, the request is retried on the next
// healthy provider instead of surfacing the error to the caller.
type Completer struct {
	completers []provider.Completer
	stats      []*ProviderStats
	strategy   Strategy
	firstPick  FirstPick

	fallback provider.Completer

	failureThreshold  int
	recoveryTimeout   time.Duration
	firstTokenTimeout time.Duration
}

type Option func(*Completer)

func WithFirstPick(pick FirstPick) Option {
	return func(c *Completer) { c.firstPick = pick }
}

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
	if strategy == nil {
		return nil, errors.New("a router strategy is required")
	}

	stats := make([]*ProviderStats, len(completers))
	for i, member := range completers {
		if member == nil {
			return nil, fmt.Errorf("router member %d is nil", i+1)
		}
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
		candidates := make([]int, 0, len(c.completers))
		for i, stat := range c.stats {
			if stat.IsCandidate(c.recoveryTimeout) {
				candidates = append(candidates, i)
			}
		}

		firstPick := c.firstPick
		var lastErr error
		for len(candidates) > 0 {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}

			var index int
			if firstPick != nil && len(candidates) > 1 {
				pick, err := firstPick(ctx, messages, options, candidates)
				if err != nil {
					yield(nil, err)
					return
				}
				index = pick
				firstPick = nil
			} else {
				index = c.strategy(candidates, c.stats)
			}
			pos := slices.Index(candidates, index)
			if pos < 0 {
				break
			}
			candidates = slices.Delete(candidates, pos, pos+1)

			stat := c.stats[index]
			acquired, probe := stat.Acquire(c.recoveryTimeout)
			if !acquired {
				continue
			}
			committed, ttft, err := c.stream(ctx, c.completers[index], messages, options, yield)
			switch {
			case ctx.Err() != nil:
				stat.Release(probe)
				if err != nil {
					yield(nil, ctx.Err())
				}
				return
			case err == nil:
				stat.RecordSuccess(ttft, probe)
				return
			case isRequestError(err):
				stat.Release(probe)
				yield(nil, err)
				return
			default:
				stat.RecordFailure(c.failureThreshold, probe, err)
				if committed {
					yield(nil, err)
					return
				}
				lastErr = err
			}
		}

		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if c.fallback != nil {
			_, _, err := c.stream(ctx, c.fallback, messages, options, yield)
			if err != nil {
				yield(nil, err)
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

// stream withholds protocol preludes until the provider produces output. Once
// committed, every following chunk and any terminal error belong to that model.
func (c *Completer) stream(ctx context.Context, member provider.Completer, messages []provider.Message, options *provider.CompleteOptions, yield func(*provider.Completion, error) bool) (committed bool, ttft time.Duration, err error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}

	attemptCtx := ctx
	var timer *time.Timer
	timeoutError := func() error {
		return &provider.ProviderError{Code: http.StatusGatewayTimeout, Message: fmt.Sprintf("no response within %s", c.firstTokenTimeout)}
	}
	if c.firstTokenTimeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		timer = time.AfterFunc(c.firstTokenTimeout, cancel)
		defer timer.Stop()
	}

	start := time.Now()
	var pending []*provider.Completion
	for chunk, streamErr := range member.Complete(attemptCtx, messages, options) {
		if attemptCtx.Err() != nil {
			break
		}
		if streamErr != nil {
			err = streamErr
			break
		}
		if !committed {
			if !hasOutput(chunk) {
				if chunk != nil {
					pending = append(pending, chunk)
				}
				continue
			}
			if ctx.Err() != nil {
				return false, 0, ctx.Err()
			}
			if timer != nil && !timer.Stop() {
				return false, 0, timeoutError()
			}
			committed, ttft = true, time.Since(start)
			for _, prelude := range pending {
				if !yield(prelude, nil) {
					return true, ttft, nil
				}
			}
			pending = nil
		}
		if !yield(chunk, nil) {
			return true, ttft, nil
		}
	}
	switch {
	case ctx.Err() != nil:
		return committed, ttft, ctx.Err()
	case !committed && timer != nil && !timer.Stop():
		return false, 0, timeoutError()
	case !committed && err == nil:
		return false, 0, errors.New("provider returned no response")
	default:
		return committed, ttft, err
	}
}

func hasOutput(completion *provider.Completion) bool {
	if completion == nil {
		return false
	}
	// A refusal or token limit can legitimately stop a turn before any text.
	if completion.Status == provider.CompletionStatusRefused || completion.Status == provider.CompletionStatusIncomplete ||
		(completion.StopReason != "" && completion.StopReason != provider.StopReasonEndTurn) {
		return true
	}
	if completion.Message == nil {
		return false
	}

	for _, part := range completion.Message.Content {
		if part.Text != "" || part.Refusal != "" || part.File != nil || part.ToolCall != nil || part.ToolResult != nil {
			return true
		}
		if r := part.Reasoning; r != nil && (r.Text() != "" || r.SummaryText() != "" || r.Signature != "") {
			return true
		}
		if c := part.Compaction; c != nil && (c.Content != "" || c.Signature != "") {
			return true
		}
		if part.CompactionTrigger || part.ConfigurationUpdate != nil || part.Instructions != nil || part.AdditionalTools != nil {
			return true
		}
	}

	return false
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
