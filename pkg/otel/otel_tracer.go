package otel

import (
	"context"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

func setupTracer(ctx context.Context, resource *resource.Resource) error {
	var err error
	var exporter trace.SpanExporter

	if strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")) == "grpc" || strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL")) == "grpc" {
		exporter, err = otlptracegrpc.New(ctx)
	} else {
		exporter, err = otlptracehttp.New(ctx)
	}

	if err != nil {
		return err
	}

	options := []trace.TracerProviderOption{
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSpanProcessor(endUserProcessor{}),
		trace.WithBatcher(exporter, trace.WithBatchTimeout(time.Second)),
		trace.WithResource(resource),
	}

	if endpoint := os.Getenv("INSIGHTS_ENDPOINT"); endpoint != "" {
		tracesURL := endpointWithDefaultPath(endpoint, "/v1/traces")
		if insights, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(tracesURL)); err == nil {
			options = append(options, trace.WithBatcher(insights, trace.WithBatchTimeout(time.Second)))
		}
	}

	provider := trace.NewTracerProvider(options...)

	otel.SetTracerProvider(provider)

	return nil
}
