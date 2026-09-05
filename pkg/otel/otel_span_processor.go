package otel

import (
	"context"

	"go.opentelemetry.io/otel/sdk/trace"
)

// endUserProcessor stamps end-user identity (user.id, user.email, user.full_name,
// session.id) onto every span at start, read from the request context. This
// propagates user.id to all spans in a trace for per-user aggregation in
// trace-native backends like Langfuse, without instrumenting each span site.
type endUserProcessor struct{}

func (endUserProcessor) OnStart(parent context.Context, s trace.ReadWriteSpan) {
	if attrs := EndUserAttrs(parent); len(attrs) > 0 {
		s.SetAttributes(attrs...)
	}
}

func (endUserProcessor) OnEnd(trace.ReadOnlySpan) {}

func (endUserProcessor) Shutdown(context.Context) error { return nil }

func (endUserProcessor) ForceFlush(context.Context) error { return nil }
