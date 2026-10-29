package budget

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type testBudget struct {
	decision *Result
	err      error
	checks   atomic.Int32
	reports  chan *Report
	entered  chan struct{}
	release  chan struct{}
}

func (b *testBudget) Check(context.Context, string) (*Result, error) {
	b.checks.Add(1)
	return b.decision, b.err
}
func (b *testBudget) Report(ctx context.Context, r *Report) error {
	if b.entered != nil {
		b.entered <- struct{}{}
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.reports <- r
	return nil
}

type testModel struct{ calls atomic.Int32 }

func (m *testModel) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls.Add(1)
		if !yield(&provider.Completion{Model: "upstream", Usage: &provider.Usage{InputTokens: 100, CacheCreationInputTokens: 50, CacheWriteTokensDetails: &provider.CacheWriteTokensDetails{StandardTokens: 20, ExtendedTokens: 30}}}, nil) {
			return
		}
		yield(&provider.Completion{Model: "upstream", Usage: &provider.Usage{OutputTokens: 9, ReasoningTokens: new(3)}}, nil)
	}
}
func (m *testModel) Embed(context.Context, []string, *provider.EmbedOptions) (*provider.Embedding, error) {
	m.calls.Add(1)
	return &provider.Embedding{Model: "upstream"}, nil
}
func (m *testModel) Decide(context.Context, *provider.DecisionInput) (*provider.Decision, error) {
	m.calls.Add(1)
	return &provider.Decision{Model: "upstream"}, nil
}
func (m *testModel) Rerank(context.Context, string, []string, *provider.RerankOptions) ([]provider.Ranking, error) {
	m.calls.Add(1)
	return nil, nil
}
func (m *testModel) Render(context.Context, string, *provider.RenderOptions) (*provider.Rendering, error) {
	m.calls.Add(1)
	return &provider.Rendering{Model: "upstream"}, nil
}
func (m *testModel) Synthesize(context.Context, string, *provider.SynthesizeOptions) iter.Seq2[*provider.Synthesis, error] {
	return func(yield func(*provider.Synthesis, error) bool) {
		m.calls.Add(1)
		yield(&provider.Synthesis{Model: "upstream"}, nil)
	}
}
func (m *testModel) Transcribe(context.Context, provider.File, *provider.TranscribeOptions) iter.Seq2[*provider.Transcription, error] {
	return func(yield func(*provider.Transcription, error) bool) {
		m.calls.Add(1)
		yield(&provider.Transcription{Model: "upstream"}, nil)
	}
}
func (m *testModel) Defaults() provider.RealtimeOptions { return provider.RealtimeOptions{} }
func (m *testModel) RealtimeCapabilities() provider.RealtimeCapabilities {
	return provider.RealtimeCapabilities{}
}
func (m *testModel) Connect(context.Context, *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	m.calls.Add(1)
	return nil, errors.New("test connect failure")
}

func TestAdmissionBeforeEveryModelType(t *testing.T) {
	for _, outcome := range []string{"missing-user", "denied", "unavailable", "no-decision", "allowed"} {
		for _, kind := range []string{"completion", "embedding", "decision", "reranking", "rendering", "synthesis", "transcription", "realtime"} {
			t.Run(outcome+"/"+kind, func(t *testing.T) {
				b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 1)}
				ctx := context.WithValue(t.Context(), auth.UserContextKey, "user")
				want := 0
				switch outcome {
				case "missing-user":
					ctx = t.Context()
					want = 401
				case "denied":
					b.decision = &Result{Message: "limit reached", ResetsAt: time.Now().Add(time.Minute)}
					want = 429
				case "unavailable":
					b.err = errors.New("offline")
					want = 503
				case "no-decision":
					b.decision = nil
					want = 503
				}
				m := &testModel{}
				var err error
				switch kind {
				case "completion":
					for _, e := range NewCompleter("test", "configured", m, b).Complete(ctx, nil, nil) {
						if e != nil {
							err = e
						}
					}
				case "embedding":
					_, err = NewEmbedder("test", "configured", m, b).Embed(ctx, nil, nil)
				case "decision":
					_, err = NewDecider("test", "configured", m, b).Decide(ctx, nil)
				case "reranking":
					_, err = NewReranker("test", "configured", m, b).Rerank(ctx, "", nil, nil)
				case "rendering":
					_, err = NewRenderer("test", "configured", m, b).Render(ctx, "", nil)
				case "synthesis":
					for _, e := range NewSynthesizer("test", "configured", m, b).Synthesize(ctx, "", nil) {
						if e != nil {
							err = e
						}
					}
				case "transcription":
					for _, e := range NewTranscriber("test", "configured", m, b).Transcribe(ctx, provider.File{}, nil) {
						if e != nil {
							err = e
						}
					}
				case "realtime":
					_, err = NewRealtime("test", "configured", m, b).Connect(ctx, nil)
				}
				if outcome != "allowed" {
					if provider.CodeFromError(err, 0) != want || m.calls.Load() != 0 || len(b.reports) != 0 {
						t.Fatalf("code=%d upstream=%d reports=%d err=%v", provider.CodeFromError(err, 0), m.calls.Load(), len(b.reports), err)
					}
					return
				}
				if m.calls.Load() != 1 || len(b.reports) != 1 {
					t.Fatalf("upstream=%d reports=%d", m.calls.Load(), len(b.reports))
				}
				r := <-b.reports
				if r.UserID != "user" || r.OperationID == "" || r.CompletedAt.IsZero() {
					t.Fatalf("incomplete accounting metadata: %+v", r)
				}
				if kind == "completion" {
					if r.Model != "upstream" || r.Usage.InputTokens != 100 || r.Usage.OutputTokens != 9 || r.Usage.CacheWriteTokensDetails.ExtendedTokens != 30 {
						t.Fatalf("usage lost: %+v", r.Usage)
					}
				} else if r.Usage != nil {
					t.Fatal("unknown usage was fabricated")
				}
			})
		}
	}
}

func TestAsyncReportsDoNotBlockAndReserveCapacity(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 1), entered: make(chan struct{}, 1), release: make(chan struct{})}
	queue := NewAsync(b, 1, 1)
	defer queue.Close()
	defer close(b.release)
	t.Cleanup(func() {
		if len(b.reports) != 1 {
			t.Error("shutdown did not drain the accepted report")
		}
	})
	m := &testModel{}
	wrapped := NewCompleter("test", "configured", m, queue)
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), auth.UserContextKey, "user"))
	defer cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range wrapped.Complete(ctx, nil, nil) {
		}
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("response blocked on report delivery")
	}
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("report worker did not start")
	}
	cancel()
	for _, err := range wrapped.Complete(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil, nil) {
		if provider.CodeFromError(err, 0) != 503 {
			t.Fatalf("full accounting queue admitted work: %v", err)
		}
	}
	if m.calls.Load() != 1 {
		t.Fatal("queue saturation reached upstream")
	}
	// The deferred release lets shutdown drain the report despite cancellation.
}

func TestSystemWorkIsNotChargedToFirstUser(t *testing.T) {
	b := &testBudget{reports: make(chan *Report, 1)}
	m := &testModel{}
	p := NewEmbedder("test", "configured", m, b)
	for _, ctx := range []context.Context{t.Context(), context.WithValue(t.Context(), auth.UserContextKey, "first-user")} {
		if _, err := p.Embed(SystemContext(ctx), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls.Load() != 2 || b.checks.Load() != 0 || len(b.reports) != 0 {
		t.Fatal("shared embeddings reached per-user accounting")
	}
}

func TestEarlyConsumerStopStillReportsObservedUsage(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 1)}
	for range NewCompleter("test", "configured", &testModel{}, b).Complete(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil, nil) {
		break
	}
	r := <-b.reports
	if r.Usage.InputTokens != 100 || r.Usage.OutputTokens != 0 {
		t.Fatalf("wrong partial usage: %+v", r.Usage)
	}
}

type testRealtime struct {
	provider.Realtime
	session *testRealtimeSession
}

func (p testRealtime) Connect(context.Context, *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	return p.session, nil
}

type testRealtimeSession struct {
	provider.RealtimeSession
	events chan provider.RealtimeEvent
	closes atomic.Int32
}

func (s *testRealtimeSession) Events() <-chan provider.RealtimeEvent { return s.events }
func (s *testRealtimeSession) Close() error                          { s.closes.Add(1); return nil }

func TestRealtimeReportsSessionUsageOnce(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 2)}
	source := &testRealtimeSession{events: make(chan provider.RealtimeEvent, 4)}
	for _, event := range []provider.RealtimeEvent{
		{Type: provider.RealtimeEventUsage, ResponseID: "one", Usage: &provider.RealtimeUsage{InputTokens: 10, OutputTokens: 2}},
		{Type: provider.RealtimeEventUsage, ResponseID: "one", Usage: &provider.RealtimeUsage{InputTokens: 10, OutputTokens: 3}},
		{Type: provider.RealtimeEventUsage, ResponseID: "two", Usage: &provider.RealtimeUsage{InputTokens: 7, OutputTokens: 5}},
	} {
		source.events <- event
	}
	close(source.events)
	session, err := NewRealtime("test", "upstream", testRealtime{session: source}, b).Connect(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for range session.Events() {
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.reports) != 1 || source.closes.Load() != 1 {
		t.Fatalf("reports=%d closes=%d", len(b.reports), source.closes.Load())
	}
	r := <-b.reports
	if r.Usage.InputTokens != 17 || r.Usage.OutputTokens != 8 {
		t.Fatalf("duplicated or lost realtime usage: %+v", r.Usage)
	}
}
