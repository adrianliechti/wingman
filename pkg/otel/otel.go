package otel

import (
	"context"
	"errors"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func Setup() error {
	if !EnableTelemetry {
		return nil
	}

	ctx := context.Background()

	attributes := []attribute.KeyValue{
		semconv.ServiceName("wingman"),
	}

	if val := os.Getenv("TELEMETRY_NAME"); val != "" {
		attributes = append(attributes, semconv.ServiceName(val))
	}

	if val := os.Getenv("TELEMETRY_VERSION"); val != "" {
		attributes = append(attributes, semconv.ServiceVersion(val))
	}

	resource, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(attributes...),
	)

	if err != nil {
		return err
	}

	if err := newPropagator(resource); err != nil {
		return err
	}

	if err := setupTracer(ctx, resource); err != nil {
		return err
	}

	if err := setupMeter(ctx, resource); err != nil {
		return err
	}

	if err := setupLogger(ctx, resource); err != nil {
		return err
	}

	if err := setupHTTP(ctx, resource); err != nil {
		return err
	}

	return nil
}

// Shutdown flushes and closes the global telemetry providers installed by Setup.
// Logs are shut down last so exporter errors can still be recorded.
func Shutdown(ctx context.Context) error {
	var err error
	for _, provider := range []any{otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetLoggerProvider()} {
		if s, ok := provider.(interface{ Shutdown(context.Context) error }); ok {
			err = errors.Join(err, s.Shutdown(ctx))
		}
	}
	return err
}
