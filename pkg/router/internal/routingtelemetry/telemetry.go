// Package routingtelemetry records low-cardinality routing decisions and
// fallback outcomes as both metrics and events on the active request span.
package routingtelemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/adrianliechti/wingman/pkg/router"

var (
	meter = otel.Meter(instrumentationName)

	decisionCount, _ = meter.Int64Counter(
		"wingman.router.decisions",
		metric.WithDescription("Number of model routing decisions"),
	)
	attemptCount, _ = meter.Int64Counter(
		"wingman.router.attempts",
		metric.WithDescription("Number of routed provider attempts"),
	)
	fallbackCount, _ = meter.Int64Counter(
		"wingman.router.fallbacks",
		metric.WithDescription("Number of routed fallbacks"),
	)
	decisionScore, _ = meter.Float64Histogram(
		"wingman.router.decision.score",
		metric.WithDescription("Classifier difficulty score at routing time"),
	)
	meaningfulTTFT, _ = meter.Float64Histogram(
		"wingman.router.time_to_first_content",
		metric.WithDescription("Seconds until a routed provider produced meaningful content"),
		metric.WithUnit("s"),
	)
)

type Decision struct {
	Router     string
	Kind       string
	Model      string
	Source     string
	Cached     bool
	Score      float64
	Candidates int
}

func RecordDecision(ctx context.Context, d Decision) {
	attrs := []attribute.KeyValue{
		attribute.String("wingman.router.name", d.Router),
		attribute.String("wingman.router.kind", d.Kind),
		attribute.String("wingman.router.model", d.Model),
		attribute.String("wingman.router.source", d.Source),
		attribute.Bool("wingman.router.cached", d.Cached),
		attribute.Int("wingman.router.candidates", d.Candidates),
	}

	decisionCount.Add(ctx, 1, metric.WithAttributes(attrs...))
	decisionScore.Record(ctx, d.Score, metric.WithAttributes(attrs...))
	trace.SpanFromContext(ctx).AddEvent("wingman.router.decision", trace.WithAttributes(attrs...))
}

type Attempt struct {
	Router    string
	Kind      string
	Model     string
	Failure   string
	Delivered bool
	TTFT      time.Duration
}

func RecordAttempt(ctx context.Context, a Attempt) {
	outcome := "success"
	if a.Failure != "" {
		outcome = a.Failure
	}

	attrs := []attribute.KeyValue{
		attribute.String("wingman.router.name", a.Router),
		attribute.String("wingman.router.kind", a.Kind),
		attribute.String("wingman.router.model", a.Model),
		attribute.String("wingman.router.outcome", outcome),
		attribute.Bool("wingman.router.delivered", a.Delivered),
	}

	attemptCount.Add(ctx, 1, metric.WithAttributes(attrs...))
	if a.Delivered && a.TTFT > 0 {
		meaningfulTTFT.Record(ctx, a.TTFT.Seconds(), metric.WithAttributes(attrs...))
	}
	trace.SpanFromContext(ctx).AddEvent("wingman.router.attempt", trace.WithAttributes(attrs...))
}

type Fallback struct {
	Router string
	Kind   string
	From   string
	To     string
	Reason string
}

func RecordFallback(ctx context.Context, f Fallback) {
	attrs := []attribute.KeyValue{
		attribute.String("wingman.router.name", f.Router),
		attribute.String("wingman.router.kind", f.Kind),
		attribute.String("wingman.router.from", f.From),
		attribute.String("wingman.router.to", f.To),
		attribute.String("wingman.router.reason", f.Reason),
	}

	fallbackCount.Add(ctx, 1, metric.WithAttributes(attrs...))
	trace.SpanFromContext(ctx).AddEvent("wingman.router.fallback", trace.WithAttributes(attrs...))
}
