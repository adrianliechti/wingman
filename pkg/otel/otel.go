package otel

import (
	"context"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

func Setup(serviceName, serviceVersion string) error {
	if !EnableTelemetry {
		return nil
	}

	ctx := context.Background()

	resource, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
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
