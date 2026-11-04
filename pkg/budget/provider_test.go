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
	"github.com/adrianliechti/wingman/pkg/researcher"
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

// gatedModel holds each call open until its gate closes.
type gatedModel struct {
	gate    chan struct{}
	entered chan struct{}
	calls   atomic.Int32
}

func (m *gatedModel) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls.Add(1)
		m.entered <- struct{}{}
		<-m.gate
		yield(&provider.Completion{Model: "upstream", Usage: &provider.Usage{InputTokens: 1}}, nil)
	}
}

func TestAsyncReportsDoNotBlockResponses(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 1), entered: make(chan struct{}, 1), release: make(chan struct{})}
	queue := NewAsync(b, 1, 1)
	defer queue.Close()
	defer close(b.release)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range NewCompleter("test", "configured", &testModel{}, queue).Complete(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil, nil) {
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
}

func TestAsyncQueueDoesNotLimitConcurrentOperations(t *testing.T) {
	const n = 6
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, n), release: make(chan struct{})}
	queue := NewAsync(b, 1, 1)
	m := &gatedModel{gate: make(chan struct{}), entered: make(chan struct{}, n)}
	wrapped := NewCompleter("test", "configured", m, queue)
	errs := make(chan error, n)
	for range n {
		go func() {
			var failure error
			for _, err := range wrapped.Complete(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil, nil) {
				if err != nil {
					failure = err
				}
			}
			errs <- failure
		}()
	}
	// All operations reach upstream at once, far beyond the queue capacity.
	for range n {
		select {
		case <-m.entered:
		case err := <-errs:
			t.Fatalf("operation rejected while others were in flight: %v", err)
		case <-time.After(time.Second):
			t.Fatal("operations were not admitted concurrently")
		}
	}
	// The worker blocks on the first report and the queue holds one more; the
	// rest wait for room instead of failing or dropping usage.
	close(m.gate)
	close(b.release)
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.reports) != n {
		t.Fatalf("reports=%d, want %d", len(b.reports), n)
	}
}

func TestAsyncShutdownDrainsInFlightOperations(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 3)}
	queue := NewAsync(b, 1, 1)
	m := &gatedModel{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	wrapped := NewCompleter("test", "configured", m, queue)
	ctx := context.WithValue(t.Context(), auth.UserContextKey, "user")
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range wrapped.Complete(ctx, nil, nil) {
		}
	}()
	<-m.entered
	shutdown := make(chan error, 1)
	go func() { shutdown <- queue.Shutdown(t.Context()) }()
	select {
	case err := <-shutdown:
		t.Fatalf("shutdown returned before the in-flight operation reported: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(m.gate)
	<-finished
	if err := <-shutdown; err != nil {
		t.Fatal(err)
	}
	if len(b.reports) != 1 {
		t.Fatalf("in-flight report lost: %d", len(b.reports))
	}
	// After shutdown, new work is rejected without touching upstream.
	for _, err := range wrapped.Complete(ctx, nil, nil) {
		if provider.CodeFromError(err, 0) != 503 || !errors.Is(err, provider.ErrRejected) {
			t.Fatalf("work admitted after shutdown: %v", err)
		}
	}
	// A late report is delivered directly instead of panicking on a closed queue.
	if err := queue.Report(ctx, &Report{OperationID: "late"}); err != nil {
		t.Fatal(err)
	}
	if len(b.reports) != 2 || m.calls.Load() != 1 {
		t.Fatalf("reports=%d upstream=%d", len(b.reports), m.calls.Load())
	}
}

func TestAdmissionIsSharedAcrossOneRequest(t *testing.T) {
	user := context.WithValue(t.Context(), auth.UserContextKey, "user")
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 5)}
	ctx := WithAdmission(user)
	m := &testModel{}
	if _, err := NewEmbedder("test", "configured", m, b).Embed(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for _, err := range NewCompleter("test", "configured", m, b).Complete(ctx, nil, nil) {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if b.checks.Load() != 1 || len(b.reports) != 3 {
		t.Fatalf("checks=%d reports=%d, want one check and every call reported", b.checks.Load(), len(b.reports))
	}

	denied := &testBudget{decision: &Result{Message: "limit reached"}, reports: make(chan *Report, 1)}
	ctx = WithAdmission(user)
	for range 2 {
		_, err := NewEmbedder("test", "configured", m, denied).Embed(ctx, nil, nil)
		if provider.CodeFromError(err, 0) != 429 || !errors.Is(err, provider.ErrRejected) {
			t.Fatalf("denial not marked as a rejected request: %v", err)
		}
	}
	if denied.checks.Load() != 1 {
		t.Fatalf("checks=%d, want the denial shared", denied.checks.Load())
	}

	// Without a request scope, each call is checked on its own.
	for range 2 {
		NewEmbedder("test", "configured", m, b).Embed(user, nil, nil)
	}
	if b.checks.Load() != 3 {
		t.Fatalf("checks=%d, want one per unscoped call", b.checks.Load())
	}
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

func receiveReport(t *testing.T, reports chan *Report) *Report {
	t.Helper()
	select {
	case r := <-reports:
		return r
	case <-time.After(time.Second):
		t.Fatal("missing report")
		return nil
	}
}

func TestRealtimeReportsEachResponse(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 4)}
	source := &testRealtimeSession{events: make(chan provider.RealtimeEvent)}
	session, err := NewRealtime("test", "upstream", testRealtime{session: source}, b).Connect(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range session.Events() {
		}
	}()
	// Snapshots for one response are cumulative and reported when it ends.
	source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: "one", Usage: &provider.RealtimeUsage{InputTokens: 10, OutputTokens: 2}}
	source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: "one", Usage: &provider.RealtimeUsage{InputTokens: 10, OutputTokens: 3}}
	source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: "one"}
	if r := receiveReport(t, b.reports); r.Usage.InputTokens != 10 || r.Usage.OutputTokens != 3 || r.UserID != "user" || r.Model != "upstream" {
		t.Fatalf("response usage = %+v", r)
	}
	// Usage without a response ID belongs to separate turns and is summed.
	for _, n := range []int{4, 6} {
		source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventUsage, Usage: &provider.RealtimeUsage{InputTokens: n}}
		if r := receiveReport(t, b.reports); r.Usage.InputTokens != n {
			t.Fatalf("turn usage = %+v, want %d input tokens", r.Usage, n)
		}
	}
	// An unfinished response is reported when the session ends.
	source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: "two", Usage: &provider.RealtimeUsage{InputTokens: 7, OutputTokens: 5}}
	close(source.events)
	if r := receiveReport(t, b.reports); r.Usage.InputTokens != 7 || r.Usage.OutputTokens != 5 {
		t.Fatalf("final usage = %+v", r.Usage)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.reports) != 0 || source.closes.Load() != 1 {
		t.Fatalf("extra reports=%d closes=%d", len(b.reports), source.closes.Load())
	}
}

func TestRealtimeSessionWithoutUsageIsStillReported(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 2)}
	source := &testRealtimeSession{events: make(chan provider.RealtimeEvent, 1)}
	source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: "one"}
	close(source.events)
	session, err := NewRealtime("test", "upstream", testRealtime{session: source}, b).Connect(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for range session.Events() {
	}
	session.Close()
	if r := receiveReport(t, b.reports); r.Usage != nil || r.UserID != "user" {
		t.Fatalf("report = %+v", r)
	}
	if len(b.reports) != 0 {
		t.Fatal("unknown usage reported twice")
	}
}

func TestRealtimeEndsSessionWhenBudgetRunsOut(t *testing.T) {
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 2)}
	source := &testRealtimeSession{events: make(chan provider.RealtimeEvent)}
	session, err := NewRealtime("test", "upstream", testRealtime{session: source}, b).Connect(context.WithValue(t.Context(), auth.UserContextKey, "user"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b.decision = &Result{Message: "limit reached"}
	source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: "one", Usage: &provider.RealtimeUsage{InputTokens: 10}}
	source.events <- provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: "one"}
	var types []provider.RealtimeEventType
	var last provider.RealtimeEvent
	timeout := time.After(2 * time.Second)
	for done := false; !done; {
		select {
		case event, ok := <-session.Events():
			if !ok {
				done = true
				break
			}
			types = append(types, event.Type)
			last = event
		case <-timeout:
			t.Fatalf("session kept running after denial: %v", types)
		}
	}
	if last.Type != provider.RealtimeEventError || last.Error == nil || last.Error.Code != "insufficient_quota" || last.Error.Message != "limit reached" {
		t.Fatalf("events=%v last=%+v", types, last)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.reports) != 1 || source.closes.Load() != 1 || b.checks.Load() != 2 {
		t.Fatalf("reports=%d closes=%d checks=%d", len(b.reports), source.closes.Load(), b.checks.Load())
	}
}

type testResearcher struct{ calls atomic.Int32 }

func (r *testResearcher) Research(context.Context, string, *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return func(yield func(*researcher.Result, error) bool) {
		r.calls.Add(1)
		if yield(&researcher.Result{Progress: "searching"}, nil) {
			yield(&researcher.Result{Content: "report"}, nil)
		}
	}
}

func TestHostedResearchIsAdmittedAndReported(t *testing.T) {
	user := context.WithValue(t.Context(), auth.UserContextKey, "user")
	b := &testBudget{decision: &Result{Allowed: true}, reports: make(chan *Report, 1)}
	r := &testResearcher{}
	result, err := researcher.Collect(NewResearcher("exa", "exa-research", r, b).Research(user, "topic", nil))
	if err != nil || result.Content != "report" {
		t.Fatalf("research = %+v, %v", result, err)
	}
	if report := receiveReport(t, b.reports); report.UserID != "user" || report.Provider != "exa" || report.Model != "exa-research" || report.Usage != nil {
		t.Fatalf("report = %+v", report)
	}
	denied := &testBudget{decision: &Result{Message: "limit reached"}, reports: make(chan *Report, 1)}
	_, err = researcher.Collect(NewResearcher("exa", "exa-research", r, denied).Research(user, "topic", nil))
	if provider.CodeFromError(err, 0) != 429 || r.calls.Load() != 1 || len(denied.reports) != 0 {
		t.Fatalf("denied research reached upstream: %v calls=%d", err, r.calls.Load())
	}
}
