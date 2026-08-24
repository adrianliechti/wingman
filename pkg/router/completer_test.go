package router

import (
	"context"
	"errors"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// mockCompleter is a configurable mock for testing
type mockCompleter struct {
	delay    time.Duration
	err      error
	response string
	calls    atomic.Int64
}

func (m *mockCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls.Add(1)

		if m.delay > 0 {
			select {
			case <-time.After(m.delay):
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}

		if m.err != nil {
			yield(nil, m.err)
			return
		}

		yield(&provider.Completion{
			ID: "test",
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					{Text: m.response},
				},
			},
		}, nil)
	}
}

// firstCandidate is a deterministic strategy that always picks the lowest index
func firstCandidate(candidates []int, _ []*ProviderStats) int {
	return candidates[0]
}

func collect(t *testing.T, c *Completer, ctx context.Context) (*provider.Completion, error) {
	t.Helper()

	messages := []provider.Message{provider.UserMessage("test")}

	var result *provider.Completion
	var lastErr error

	for completion, err := range c.Complete(ctx, messages, nil) {
		if err != nil {
			lastErr = err
			continue
		}

		result = completion
	}

	return result, lastErr
}

func TestNewCompleter(t *testing.T) {
	t.Run("requires at least one completer", func(t *testing.T) {
		_, err := NewCompleter(nil, firstCandidate)
		if err == nil {
			t.Error("expected error for empty completers")
		}
	})
}

func TestComplete(t *testing.T) {
	t.Run("routes to available provider", func(t *testing.T) {
		mock := &mockCompleter{response: "hello"}
		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate)

		result, err := collect(t, c, context.Background())

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil || result.Message.Text() != "hello" {
			t.Errorf("expected 'hello', got %v", result)
		}
	})

	t.Run("fails over to next provider before first token", func(t *testing.T) {
		failing := &mockCompleter{err: errors.New("provider error")}
		healthy := &mockCompleter{response: "ok"}

		c, _ := NewCompleter([]provider.Completer{failing, healthy}, firstCandidate)

		result, err := collect(t, c, context.Background())

		if err != nil {
			t.Fatalf("expected failover to hide the error, got: %v", err)
		}

		if result == nil || result.Message.Text() != "ok" {
			t.Errorf("expected 'ok' from healthy provider, got %v", result)
		}

		if failing.calls.Load() != 1 || healthy.calls.Load() != 1 {
			t.Errorf("expected both providers tried once, got %d and %d", failing.calls.Load(), healthy.calls.Load())
		}
	})

	t.Run("yields last error when all providers fail", func(t *testing.T) {
		mock := &mockCompleter{err: errors.New("provider error")}
		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate)

		result, err := collect(t, c, context.Background())

		if result != nil {
			t.Errorf("expected no result, got %v", result)
		}

		if err == nil || err.Error() != "provider error" {
			t.Errorf("expected provider error, got %v", err)
		}
	})

	t.Run("opens circuit after threshold failures", func(t *testing.T) {
		mock := &mockCompleter{err: errors.New("provider error")}
		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate)

		ctx := context.Background()

		for range DefaultFailureThreshold {
			collect(t, c, ctx)
		}

		if state := c.stats[0].Metrics().State; state != CircuitOpen {
			t.Errorf("expected circuit open after %d failures, got %v", DefaultFailureThreshold, state)
		}
	})

	t.Run("uses fallback when all providers unavailable", func(t *testing.T) {
		failing := &mockCompleter{err: errors.New("provider error")}
		fallback := &mockCompleter{response: "fallback"}

		c, _ := NewCompleter([]provider.Completer{failing}, firstCandidate,
			WithFallback(fallback),
			WithRecoveryTimeout(time.Hour),
		)

		ctx := context.Background()

		for range DefaultFailureThreshold {
			collect(t, c, ctx)
		}

		result, err := collect(t, c, ctx)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil || result.Message.Text() != "fallback" {
			t.Errorf("expected 'fallback', got %v", result)
		}

		if failing.calls.Load() != DefaultFailureThreshold {
			t.Errorf("open circuit should not receive calls, got %d", failing.calls.Load())
		}
	})
}

func TestFirstTokenTimeout(t *testing.T) {
	t.Run("fails over when first token times out", func(t *testing.T) {
		slow := &mockCompleter{response: "slow", delay: time.Second}
		fast := &mockCompleter{response: "fast"}

		c, _ := NewCompleter([]provider.Completer{slow, fast}, firstCandidate,
			WithFirstTokenTimeout(20*time.Millisecond),
		)

		start := time.Now()
		result, err := collect(t, c, context.Background())

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil || result.Message.Text() != "fast" {
			t.Errorf("expected 'fast' after timeout failover, got %v", result)
		}

		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("failover took too long: %v", elapsed)
		}

		if rate := c.stats[0].Metrics().ErrorRate; rate <= 0 {
			t.Error("expected timeout to count as failure for the slow provider")
		}
	})

	t.Run("does not interrupt delivery after first token", func(t *testing.T) {
		mock := &mockCompleter{response: "ok", delay: 10 * time.Millisecond}

		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate,
			WithFirstTokenTimeout(time.Minute),
		)

		result, err := collect(t, c, context.Background())

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil || result.Message.Text() != "ok" {
			t.Errorf("expected 'ok', got %v", result)
		}
	})
}

func TestCancellation(t *testing.T) {
	t.Run("caller cancellation is not a provider failure", func(t *testing.T) {
		mock := &mockCompleter{response: "ok", delay: time.Second}
		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate)

		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(10*time.Millisecond, cancel)

		_, err := collect(t, c, ctx)

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}

		metrics := c.stats[0].Metrics()

		if metrics.ErrorRate != 0 {
			t.Errorf("cancellation must not count as failure, got error rate %v", metrics.ErrorRate)
		}

		if metrics.State != CircuitClosed {
			t.Errorf("expected circuit closed, got %v", metrics.State)
		}

		if inflight := c.stats[0].Metrics().Inflight; inflight != 0 {
			t.Errorf("expected 0 inflight after cancel, got %d", inflight)
		}
	})
}

func TestCircuitRecovery(t *testing.T) {
	t.Run("recovers circuit after timeout", func(t *testing.T) {
		mock := &mockCompleter{err: errors.New("provider error")}

		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate,
			WithRecoveryTimeout(10*time.Millisecond),
		)

		ctx := context.Background()

		for range DefaultFailureThreshold {
			collect(t, c, ctx)
		}

		if state := c.stats[0].Metrics().State; state != CircuitOpen {
			t.Fatal("expected circuit to be open")
		}

		time.Sleep(20 * time.Millisecond)

		mock.err = nil
		mock.response = "recovered"

		result, err := collect(t, c, ctx)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil || result.Message.Text() != "recovered" {
			t.Errorf("expected 'recovered', got %v", result)
		}

		if state := c.stats[0].Metrics().State; state != CircuitClosed {
			t.Errorf("expected circuit closed after recovery, got %v", state)
		}
	})

	t.Run("retry-after extends the recovery wait", func(t *testing.T) {
		mock := &mockCompleter{err: &provider.ProviderError{Code: 429, Message: "rate limited", RetryAfter: time.Hour}}

		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate,
			WithRecoveryTimeout(10*time.Millisecond),
		)

		ctx := context.Background()

		for range DefaultFailureThreshold {
			collect(t, c, ctx)
		}

		if state := c.stats[0].Metrics().State; state != CircuitOpen {
			t.Fatal("expected circuit to be open")
		}

		time.Sleep(20 * time.Millisecond)

		mock.calls.Store(0)
		collect(t, c, ctx)

		if calls := mock.calls.Load(); calls != 0 {
			t.Errorf("expected no probe before retry-after elapses, got %d calls", calls)
		}
	})

	t.Run("allows a single half-open probe", func(t *testing.T) {
		mock := &mockCompleter{err: errors.New("provider error")}

		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate,
			WithRecoveryTimeout(10*time.Millisecond),
		)

		ctx := context.Background()

		for range DefaultFailureThreshold {
			collect(t, c, ctx)
		}

		time.Sleep(20 * time.Millisecond)

		mock.err = nil
		mock.response = "ok"
		mock.delay = 50 * time.Millisecond
		mock.calls.Store(0)

		var wg sync.WaitGroup

		wg.Go(func() {
			collect(t, c, ctx)
		})

		time.Sleep(10 * time.Millisecond)

		// While the probe is inflight, no other request may reach the provider
		_, err := collect(t, c, ctx)

		if err == nil {
			t.Error("expected error while probe is inflight")
		}

		wg.Wait()

		if calls := mock.calls.Load(); calls != 1 {
			t.Errorf("expected a single probe request, got %d", calls)
		}
	})
}

func TestInflightTracking(t *testing.T) {
	t.Run("tracks inflight requests correctly", func(t *testing.T) {
		mock := &mockCompleter{response: "ok", delay: 50 * time.Millisecond}
		c, _ := NewCompleter([]provider.Completer{mock}, firstCandidate)

		var wg sync.WaitGroup

		wg.Go(func() {
			collect(t, c, context.Background())
		})

		time.Sleep(10 * time.Millisecond)

		if inflight := c.stats[0].Metrics().Inflight; inflight != 1 {
			t.Errorf("expected 1 inflight request, got %d", inflight)
		}

		wg.Wait()

		if inflight := c.stats[0].Metrics().Inflight; inflight != 0 {
			t.Errorf("expected 0 inflight requests after completion, got %d", inflight)
		}
	})
}

// completerFunc allows ad-hoc completer behaviors in tests
type completerFunc func(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error]

func (f completerFunc) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return f(ctx, messages, options)
}

func TestClientErrors(t *testing.T) {
	t.Run("4xx errors do not fail over or count as provider failure", func(t *testing.T) {
		badRequest := &provider.ProviderError{Code: 400, Message: "invalid request"}

		failing := &mockCompleter{err: badRequest}
		healthy := &mockCompleter{response: "ok"}

		c, _ := NewCompleter([]provider.Completer{failing, healthy}, firstCandidate)

		result, err := collect(t, c, context.Background())

		if result != nil {
			t.Errorf("expected no result, got %v", result)
		}

		if !errors.Is(err, badRequest) {
			t.Errorf("expected the 400 error surfaced directly, got %v", err)
		}

		if healthy.calls.Load() != 0 {
			t.Error("client error must not fail over to the next provider")
		}

		metrics := c.stats[0].Metrics()

		if metrics.ErrorRate != 0 || metrics.Inflight != 0 {
			t.Errorf("client error must not count against provider health, got rate %v inflight %d", metrics.ErrorRate, metrics.Inflight)
		}
	})

	t.Run("401 fails over and counts as provider failure", func(t *testing.T) {
		unauthorized := &mockCompleter{err: &provider.ProviderError{Code: 401, Message: "invalid api key"}}
		healthy := &mockCompleter{response: "ok"}

		c, _ := NewCompleter([]provider.Completer{unauthorized, healthy}, firstCandidate)

		result, err := collect(t, c, context.Background())

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil || result.Message.Text() != "ok" {
			t.Errorf("expected failover to healthy provider, got %v", result)
		}

		if rate := c.stats[0].Metrics().ErrorRate; rate <= 0 {
			t.Error("expected auth error to count as provider failure")
		}
	})

	t.Run("429 still fails over", func(t *testing.T) {
		limited := &mockCompleter{err: &provider.ProviderError{Code: 429, Message: "rate limited"}}
		healthy := &mockCompleter{response: "ok"}

		c, _ := NewCompleter([]provider.Completer{limited, healthy}, firstCandidate)

		result, err := collect(t, c, context.Background())

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil || result.Message.Text() != "ok" {
			t.Errorf("expected failover to healthy provider, got %v", result)
		}

		if rate := c.stats[0].Metrics().ErrorRate; rate <= 0 {
			t.Error("expected rate limit to count as provider failure")
		}
	})
}

func TestMidStreamFailure(t *testing.T) {
	t.Run("stream ending in error counts as provider failure", func(t *testing.T) {
		broken := completerFunc(func(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
			return func(yield func(*provider.Completion, error) bool) {
				completion := &provider.Completion{
					ID: "test",
					Message: &provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: []provider.Content{{Text: "partial"}},
					},
				}

				if !yield(completion, nil) {
					return
				}

				yield(nil, errors.New("stream interrupted"))
			}
		})

		c, _ := NewCompleter([]provider.Completer{broken}, firstCandidate)

		result, err := collect(t, c, context.Background())

		if result == nil || result.Message.Text() != "partial" {
			t.Errorf("expected partial output delivered, got %v", result)
		}

		if err == nil {
			t.Error("expected the mid-stream error passed to the caller")
		}

		if rate := c.stats[0].Metrics().ErrorRate; rate <= 0 {
			t.Error("expected interrupted stream to count as provider failure")
		}
	})
}
