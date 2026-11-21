package router

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Strategy selects the next provider index from the given candidates.
// candidates is never empty; stats is indexed by provider, not by candidate.
type Strategy func(candidates []int, stats []*ProviderStats) int

// FirstPick selects a healthy candidate for the first attempt of a request.
// It may reorder the candidates to guide the router's strategy on retries.
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

	// distinct marks members that are different models rather than replicas.
	distinct bool

	failureThreshold  int
	recoveryTimeout   time.Duration
	firstTokenTimeout time.Duration
}

type Option func(*Completer)

func WithFirstPick(pick FirstPick) Option {
	return func(c *Completer) { c.firstPick = pick }
}

// WithDistinctMembers marks the members as different models rather than
// replicas of one. A request that one member rejects (context too long, an
// input or parameter it does not support) is then tried on the others before
// the rejection reaches the caller.
func WithDistinctMembers() Option {
	return func(c *Completer) { c.distinct = true }
}

// WithFallback sets a fallback completer used when all primary providers are unavailable
func WithFallback(fallback provider.Completer) Option {
	return func(c *Completer) {
		c.fallback = fallback
	}
}

// WithFirstTokenTimeout bounds the wait for a provider to start answering. A
// provider that shows no sign of it within this window is recorded as failed
// and the request fails over to the next provider. Zero disables the deadline.
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

// Circuit returns the failure threshold and recovery timeout of the members'
// circuits, for health that is tracked alongside them.
func (c *Completer) Circuit() (failureThreshold int, recoveryTimeout time.Duration) {
	return c.failureThreshold, c.recoveryTimeout
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
		// rejection is the first request error of a distinct member. It is
		// returned only when no other member could serve the request.
		var lastErr, rejection error
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
			case err == nil:
				stat.RecordSuccess(ttft, probe)
				return
			case ctx.Err() != nil:
				// The caller went away, which says nothing about the provider.
				stat.Release(probe)
				yield(nil, ctx.Err())
				return
			case isRequestError(err):
				stat.Release(probe)
				if committed || !c.distinct || errors.Is(err, provider.ErrRejected) {
					yield(nil, err)
					return
				}
				if rejection == nil {
					rejection = err
				}
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
			// Nothing is left to fail over to, so the fallback streams as it
			// is: no first-token deadline and no withheld chunks.
			for completion, err := range c.fallback.Complete(ctx, messages, options) {
				if !yield(completion, err) {
					return
				}
			}
			return
		}

		// A provider failure may pass, so it is reported before a rejection.
		if lastErr == nil {
			lastErr = rejection
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
// The first-token deadline is met as soon as the model starts to generate,
// which may be long before its first output: a model can reason in silence.
func (c *Completer) stream(ctx context.Context, member provider.Completer, messages []provider.Message, options *provider.CompleteOptions, yield func(*provider.Completion, error) bool) (committed bool, ttft time.Duration, err error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}

	attemptCtx := ctx
	var wait *deadline
	if c.firstTokenTimeout > 0 {
		// A router that calls this one would otherwise give up at the same
		// moment and take the remaining members with it.
		outer, _ := ctx.Value(deadlineKey{}).(*deadline)
		outer.pause()
		defer outer.resume()

		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		wait = newDeadline(c.firstTokenTimeout, cancel)
		defer wait.meet()
		attemptCtx = context.WithValue(attemptCtx, deadlineKey{}, wait)
	}

	start := time.Now()
	var pending []*provider.Completion
	started, notified, interrupted := false, false, false
	for chunk, streamErr := range member.Complete(attemptCtx, messages, options) {
		if attemptCtx.Err() != nil {
			interrupted = true
			break
		}
		if streamErr != nil {
			err = streamErr
			break
		}
		if !started && hasActivity(chunk) {
			if !wait.meet() {
				interrupted = true
				break
			}
			started, ttft = true, time.Since(start)
		}
		if !committed {
			if !hasOutput(chunk) {
				if chunk != nil {
					pending = append(pending, chunk)
				}
				if started && !notified {
					notified = true
					provider.NotifyPending(ctx)
				}
				continue
			}
			committed = true
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
	case interrupted && ctx.Err() != nil:
		return committed, ttft, ctx.Err()
	case interrupted || !wait.meet():
		// Besides the caller, only the deadline cancels an attempt. A member
		// may also end its stream quietly once it is cancelled.
		return false, 0, &provider.ProviderError{Code: http.StatusGatewayTimeout, Message: fmt.Sprintf("no response within %s", c.firstTokenTimeout)}
	case err != nil:
		return committed, ttft, err
	case committed:
		// The stream is complete, even if the caller is gone by now.
		return true, ttft, nil
	case ctx.Err() != nil:
		return false, 0, ctx.Err()
	case len(pending) == 0:
		return false, 0, errors.New("provider returned no response")
	}

	// The model ended its turn without content. That is its answer, not a
	// failure that another member could repair.
	for _, chunk := range pending {
		if !yield(chunk, nil) {
			break
		}
	}
	return true, ttft, nil
}

type deadlineKey struct{}

// deadline bounds the wait for a member to start answering. It travels in the
// attempt's context: a nested router pauses it while its own deadline runs, so
// the router closest to the model decides when to give up and can still fail
// over among its own members.
type deadline struct {
	mu      sync.Mutex
	timeout time.Duration
	cancel  context.CancelFunc
	timer   *time.Timer
	round   int
	paused  bool
	met     bool
	expired bool
}

func newDeadline(timeout time.Duration, cancel context.CancelFunc) *deadline {
	d := &deadline{timeout: timeout, cancel: cancel}
	d.arm()
	return d
}

// arm starts the timer. A timer of an earlier round no longer expires the wait.
func (d *deadline) arm() {
	d.round++
	round := d.round
	d.timer = time.AfterFunc(d.timeout, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.met || d.round != round {
			return
		}
		d.expired = true
		d.cancel()
	})
}

// meet ends the wait and reports whether the member answered in time.
func (d *deadline) meet() bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.expired {
		return false
	}
	d.met = true
	d.timer.Stop()
	return true
}

// pause stops the wait while a nested router runs a deadline of its own.
func (d *deadline) pause() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.met || d.expired || d.paused {
		return
	}
	d.paused = true
	d.round++
	d.timer.Stop()
}

// resume restarts the wait in full, for whatever the nested router tries next.
func (d *deadline) resume() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.met || d.expired || !d.paused {
		return
	}
	d.paused = false
	d.arm()
}

// hasActivity reports whether the model has started to generate. Any content
// part counts, including the empty start of a text or reasoning item.
func hasActivity(completion *provider.Completion) bool {
	return hasOutput(completion) || (completion != nil && completion.Message != nil && len(completion.Message.Content) > 0)
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
