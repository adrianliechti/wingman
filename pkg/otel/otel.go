package otel

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
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
