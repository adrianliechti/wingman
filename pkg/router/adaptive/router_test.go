package adaptive

import (
	"context"
	"errors"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
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
			time.Sleep(m.delay)
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

func TestNewCompleter(t *testing.T) {
	t.Run("requires at least one completer", func(t *testing.T) {
		_, err := NewCompleter()
		if err == nil {
			t.Error("expected error for empty completers")
		}
	})

	t.Run("creates completer with providers", func(t *testing.T) {
		mock := &mockCompleter{response: "hello"}
		c, err := NewCompleter(mock)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c == nil {
			t.Error("expected non-nil completer")
		}
	})
}

func TestComplete(t *testing.T) {
	t.Run("routes to available provider", func(t *testing.T) {
		mock := &mockCompleter{response: "hello"}
		c, _ := NewCompleter(mock)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		var result *provider.Completion
		for completion, err := range c.Complete(ctx, messages, nil) {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result = completion
		}

		if result == nil {
			t.Fatal("expected completion result")
		}

		if result.Message.Text() != "hello" {
			t.Errorf("expected 'hello', got '%s'", result.Message.Text())
		}
	})

	t.Run("records failure on error", func(t *testing.T) {
		mock := &mockCompleter{err: errors.New("provider error")}
		c, _ := NewCompleter(mock)
		comp := c.(*Completer)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		for _, err := range c.Complete(ctx, messages, nil) {
			if err == nil {
				t.Error("expected error")
			}
		}

		state, _, _, failures, _ := comp.stats[0].GetMetrics()
		if failures != 1 {
			t.Errorf("expected 1 failure, got %d", failures)
		}
		if state != router.CircuitClosed {
			t.Errorf("expected circuit closed after 1 failure")
		}
	})

	t.Run("opens circuit after threshold failures", func(t *testing.T) {
		mock := &mockCompleter{err: errors.New("provider error")}
		c, _ := NewCompleter(mock)
		comp := c.(*Completer)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		// Trigger failures to open circuit
		for i := 0; i < router.DefaultFailureThreshold; i++ {
			for range c.Complete(ctx, messages, nil) {
			}
		}

		state, _, _, _, _ := comp.stats[0].GetMetrics()
		if state != router.CircuitOpen {
			t.Errorf("expected circuit open after %d failures", router.DefaultFailureThreshold)
		}
	})
}

func TestProviderSelection(t *testing.T) {
	t.Run("prefers lower TTFT provider", func(t *testing.T) {
		slow := &mockCompleter{response: "slow", delay: 100 * time.Millisecond}
		fast := &mockCompleter{response: "fast", delay: 10 * time.Millisecond}

		c, _ := NewCompleter(slow, fast)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		// Warm up both providers to establish TTFT metrics
		for range c.Complete(ctx, messages, nil) {
		}
		for range c.Complete(ctx, messages, nil) {
		}

		// Reset call counts
		slow.calls.Store(0)
		fast.calls.Store(0)

		// Run multiple requests and count distribution
		for i := 0; i < 100; i++ {
			for range c.Complete(ctx, messages, nil) {
			}
		}

		fastCalls := fast.calls.Load()
		slowCalls := slow.calls.Load()

		// Fast provider should get significantly more calls
		if fastCalls <= slowCalls {
			t.Errorf("expected fast provider (%d calls) to be preferred over slow (%d calls)",
				fastCalls, slowCalls)
		}
	})

	t.Run("skips open circuit providers", func(t *testing.T) {
		failing := &mockCompleter{err: errors.New("error")}
		healthy := &mockCompleter{response: "ok"}

		c, _ := NewCompleter(failing, healthy)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		// Open circuit on first provider
		for i := 0; i < router.DefaultFailureThreshold; i++ {
			for range c.Complete(ctx, messages, nil) {
			}
		}

		// Reset counts
		failing.calls.Store(0)
		healthy.calls.Store(0)

		// Next requests should only go to healthy provider
		for i := 0; i < 10; i++ {
			for range c.Complete(ctx, messages, nil) {
			}
		}

		if failing.calls.Load() > 0 {
			t.Error("failing provider should not receive calls while circuit is open")
		}

		if healthy.calls.Load() != 10 {
			t.Errorf("expected 10 calls to healthy provider, got %d", healthy.calls.Load())
		}
	})

	t.Run("returns error when all providers unavailable", func(t *testing.T) {
		failing := &mockCompleter{err: errors.New("error")}
		c, _ := NewCompleter(failing)
		comp := c.(*Completer)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		// Open circuit
		for i := 0; i < router.DefaultFailureThreshold; i++ {
			for range c.Complete(ctx, messages, nil) {
			}
		}

		// Set a very long recovery timeout to prevent half-open
		comp.recoveryTimeout = time.Hour

		var gotError bool
		for _, err := range c.Complete(ctx, messages, nil) {
			if err != nil {
				gotError = true
			}
		}

		if !gotError {
			t.Error("expected error when all providers unavailable")
		}
	})
}

func TestInflightTracking(t *testing.T) {
	t.Run("tracks inflight requests correctly", func(t *testing.T) {
		// Create a provider with some delay to observe inflight behavior
		mock := &mockCompleter{response: "ok", delay: 10 * time.Millisecond}

		c, _ := NewCompleter(mock)
		comp := c.(*Completer)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		// Start a request in the background
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range c.Complete(ctx, messages, nil) {
			}
		}()

		// Give it a moment to start
		time.Sleep(2 * time.Millisecond)

		// Check inflight count
		inflight := comp.stats[0].GetInflight()
		if inflight != 1 {
			t.Errorf("expected 1 inflight request, got %d", inflight)
		}

		wg.Wait()

		// After completion, inflight should be 0
		inflight = comp.stats[0].GetInflight()
		if inflight != 0 {
			t.Errorf("expected 0 inflight requests after completion, got %d", inflight)
		}
	})

	t.Run("inflight affects provider selection", func(t *testing.T) {
		// Two providers with similar TTFT
		mock1 := &mockCompleter{response: "one", delay: 5 * time.Millisecond}
		mock2 := &mockCompleter{response: "two", delay: 5 * time.Millisecond}

		c, _ := NewCompleter(mock1, mock2)
		comp := c.(*Completer)

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		// Warm up both providers
		for range c.Complete(ctx, messages, nil) {
		}
		for range c.Complete(ctx, messages, nil) {
		}

		// Manually set inflight on first provider to simulate load
		comp.stats[0].AddInflight(10)

		// Reset counts
		mock1.calls.Store(0)
		mock2.calls.Store(0)

		// Run some requests - should prefer the less loaded provider
		for i := 0; i < 20; i++ {
			for range c.Complete(ctx, messages, nil) {
			}
		}

		// Provider 2 should get more calls due to lower inflight
		calls1 := mock1.calls.Load()
		calls2 := mock2.calls.Load()

		if calls2 <= calls1 {
			t.Errorf("expected provider 2 (inflight=0) to get more calls than provider 1 (inflight=10): got %d vs %d",
				calls2, calls1)
		}

		// Reset the artificial inflight
		comp.stats[0].AddInflight(-10)
	})
}

func TestCircuitRecovery(t *testing.T) {
	t.Run("recovers circuit after timeout", func(t *testing.T) {
		mock := &mockCompleter{err: errors.New("error")}
		c, _ := NewCompleter(mock)
		comp := c.(*Completer)

		// Use short recovery timeout for test
		comp.recoveryTimeout = 10 * time.Millisecond

		ctx := context.Background()
		messages := []provider.Message{provider.UserMessage("test")}

		// Open circuit
		for i := 0; i < router.DefaultFailureThreshold; i++ {
			for range c.Complete(ctx, messages, nil) {
			}
		}

		state, _, _, _, _ := comp.stats[0].GetMetrics()
		if state != router.CircuitOpen {
			t.Fatal("expected circuit to be open")
		}

		// Wait for recovery timeout
		time.Sleep(20 * time.Millisecond)

		// Fix the provider
		mock.err = nil
		mock.response = "recovered"

		// Should transition to half-open and then closed on success
		var result *provider.Completion
		for completion, err := range c.Complete(ctx, messages, nil) {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result = completion
		}

		if result.Message.Text() != "recovered" {
			t.Errorf("expected 'recovered', got '%s'", result.Message.Text())
		}

		state, _, _, _, _ = comp.stats[0].GetMetrics()
		if state != router.CircuitClosed {
			t.Errorf("expected circuit closed after recovery, got %v", state)
		}
	})
}
