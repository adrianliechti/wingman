// Package executor owns the streaming and failover contract shared by model
// routers. In particular, a provider is not committed until it produces
// meaningful message content; protocol preludes such as a role-only delta are
// buffered so another provider can still be tried safely.
package executor

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// FailureKind describes why an attempt ended. Only provider, timeout and empty
// failures are safe to retry on another candidate.
type FailureKind string

const (
	FailureNone     FailureKind = ""
	FailureCaller   FailureKind = "caller"
	FailureRequest  FailureKind = "request"
	FailureProvider FailureKind = "provider"
	FailureTimeout  FailureKind = "timeout"
	FailureEmpty    FailureKind = "empty"
)

// Result is the outcome of one provider attempt.
type Result struct {
	Delivered bool
	TTFT      time.Duration
	Err       error
	Failure   FailureKind
}

// Retryable reports whether the attempt may safely move to another provider.
func (r Result) Retryable() bool {
	return !r.Delivered && (r.Failure == FailureProvider || r.Failure == FailureTimeout || r.Failure == FailureEmpty)
}

// Candidate is one provider in an ordered fallback chain.
type Candidate struct {
	ID        string
	Completer provider.Completer
}

// Hooks observes attempts without changing executor behavior.
type Hooks struct {
	Attempt  func(Candidate, Result)
	Fallback func(Candidate, Candidate, Result)
}

// Complete tries candidates in order until one commits output or a
// non-retryable failure occurs.
func Complete(ctx context.Context, candidates []Candidate, messages []provider.Message, options *provider.CompleteOptions, firstTokenTimeout time.Duration, hooks Hooks) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if len(candidates) == 0 {
			yield(nil, &provider.ProviderError{Code: http.StatusServiceUnavailable, Message: "no routing candidates are available"})
			return
		}

		var last Result

		for i, candidate := range candidates {
			last = Attempt(ctx, candidate.Completer, messages, options, firstTokenTimeout, yield)

			if hooks.Attempt != nil {
				hooks.Attempt(candidate, last)
			}

			if last.Delivered {
				return
			}

			if !last.Retryable() {
				if last.Err != nil {
					yield(nil, last.Err)
				}
				return
			}

			if i+1 < len(candidates) {
				if hooks.Fallback != nil {
					hooks.Fallback(candidate, candidates[i+1], last)
				}
				continue
			}

			if last.Err != nil {
				yield(nil, last.Err)
			}
			return
		}
	}
}

// Attempt runs one provider stream. Non-content preludes are withheld until
// the first meaningful delta, which is the point after which failover would
// risk mixing two providers' answers.
func Attempt(ctx context.Context, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions, firstTokenTimeout time.Duration, yield func(*provider.Completion, error) bool) Result {
	if err := ctx.Err(); err != nil {
		return Result{Err: err, Failure: FailureCaller}
	}

	attemptCtx := ctx
	var cancel context.CancelFunc
	var timer *time.Timer
	var timedOut atomic.Bool

	if firstTokenTimeout > 0 {
		attemptCtx, cancel = context.WithCancel(ctx)
		defer cancel()

		timer = time.AfterFunc(firstTokenTimeout, func() {
			timedOut.Store(true)
			cancel()
		})
		defer timer.Stop()
	}

	start := time.Now()
	prelude := make([]*provider.Completion, 0, 1)
	result := Result{}

	for completion, err := range completer.Complete(attemptCtx, messages, options) {
		if !result.Delivered && ctx.Err() != nil {
			return Result{Err: ctx.Err(), Failure: FailureCaller}
		}

		meaningful := completion != nil && completion.Message != nil && len(completion.Message.Content) > 0

		if !result.Delivered && meaningful {
			result.Delivered = true
			result.TTFT = time.Since(start)

			if timer != nil {
				timer.Stop()
			}

			for _, buffered := range prelude {
				if !yield(buffered, nil) {
					return result
				}
			}
			prelude = nil
		}

		if !result.Delivered {
			if err != nil {
				return classifyFailure(ctx, timedOut.Load(), firstTokenTimeout, err)
			}

			if completion != nil {
				prelude = append(prelude, completion)
			}
			continue
		}

		if err != nil {
			result.Err = err
			result.Failure = FailureProvider
		}

		if !yield(completion, err) {
			return result
		}
	}

	if result.Delivered {
		if ctx.Err() != nil {
			result.Err = ctx.Err()
			result.Failure = FailureCaller
		}
		return result
	}

	if err := ctx.Err(); err != nil {
		return Result{Err: err, Failure: FailureCaller}
	}

	if timedOut.Load() {
		return timeoutResult(firstTokenTimeout, attemptCtx.Err())
	}

	err := errors.New("provider returned no response")
	return Result{Err: err, Failure: FailureEmpty}
}

func classifyFailure(ctx context.Context, timedOut bool, firstTokenTimeout time.Duration, err error) Result {
	if ctx.Err() != nil {
		return Result{Err: ctx.Err(), Failure: FailureCaller}
	}

	if timedOut {
		return timeoutResult(firstTokenTimeout, err)
	}

	if isRequestError(err) {
		return Result{Err: err, Failure: FailureRequest}
	}

	return Result{Err: err, Failure: FailureProvider}
}

func timeoutResult(timeout time.Duration, err error) Result {
	return Result{
		Err: &provider.ProviderError{
			Code:    http.StatusGatewayTimeout,
			Message: fmt.Sprintf("no response within %s", timeout),
			Err:     err,
		},
		Failure: FailureTimeout,
	}
}

// isRequestError reports request failures that another provider cannot fix.
// Provider-specific auth, deployment, timeout and quota failures remain
// retryable.
func isRequestError(err error) bool {
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
