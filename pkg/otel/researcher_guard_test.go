package otel_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/researcher"
	api "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type countingGuard struct {
	calls int
}

func (g *countingGuard) Check(context.Context, string, *guard.CheckOptions) (*guard.Result, error) {
	g.calls++
	return &guard.Result{}, nil
}

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdk.NewTracerProvider(sdk.WithSpanProcessor(recorder))
	previous := api.GetTracerProvider()
	api.SetTracerProvider(provider)
	t.Cleanup(func() {
		api.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

type testContextKey struct{}

type researchFunc func(context.Context, string, *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error]

func (f researchFunc) Research(ctx context.Context, input string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return f(ctx, input, options)
}

func TestGuardedResearchStreamLifetime(t *testing.T) {
	sentinel := errors.New("upstream failed")
	for _, tc := range []struct {
		name      string
		err       error
		stopEarly bool
	}{
		{name: "complete"},
		{name: "error", err: sentinel},
		{name: "early stop", stopEarly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := recordSpans(t)
			g := &countingGuard{}
			options := &researcher.ResearchOptions{}
			progress := &researcher.Result{Progress: "working"}
			final := &researcher.Result{Content: "report"}
			called, closed := false, false
			raw := researchFunc(func(ctx context.Context, input string, got *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
				called = true
				if input != "instructions" || got != options || ctx.Value(testContextKey{}) != "value" || !trace.SpanFromContext(ctx).IsRecording() || g.calls != 1 {
					t.Fatal("arguments or tracing context lost")
				}
				return func(yield func(*researcher.Result, error) bool) {
					defer func() { closed = true }()
					if len(recorder.Ended()) != 0 {
						t.Fatal("span ended before stream consumption")
					}
					if !yield(progress, nil) {
						return
					}
					if tc.err != nil {
						yield(nil, tc.err)
						return
					}
					yield(final, nil)
				}
			})
			p := otel.NewResearcher("test", "web", researcher.NewGuard(g, raw))
			stream := p.Research(context.WithValue(t.Context(), testContextKey{}, "value"), "instructions", options)
			if called || g.calls != 0 || len(recorder.Started()) != 0 {
				t.Fatal("stream was not lazy")
			}
			count := 0
			for result, err := range stream {
				count++
				if count == 1 && (result != progress || err != nil) {
					t.Fatal("progress changed")
				}
				if count == 2 {
					if !errors.Is(err, tc.err) {
						t.Fatalf("error = %v, want %v", err, tc.err)
					}
					if tc.err == nil && result != final {
						t.Fatal("final result changed")
					}
				}
				if tc.stopEarly {
					break
				}
			}
			want := 2
			if tc.stopEarly {
				want = 1
			}
			if count != want || !closed || len(recorder.Ended()) != 1 {
				t.Fatalf("items=%d closed=%v spans=%d", count, closed, len(recorder.Ended()))
			}
			if (recorder.Ended()[0].Status().Code == codes.Error) != (tc.err != nil) {
				t.Fatal("incorrect span error status")
			}
		})
	}
}
