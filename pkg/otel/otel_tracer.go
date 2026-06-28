package otel

import (
	"context"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"

	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

func setupTracer(ctx context.Context, resource *sdkresource.Resource) error {
	var err error
	var exporter sdktrace.SpanExporter

	if strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")) == "grpc" || strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL")) == "grpc" {
		exporter, err = otlptracegrpc.New(ctx)
	} else {
		exporter, err = otlptracehttp.New(ctx)
	}

	if err != nil {
		return err
	}

	options := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(endUserProcessor{}),
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(time.Second)),
		sdktrace.WithResource(resource),
	}

	if endpoint := os.Getenv("INSIGHTS_ENDPOINT"); endpoint != "" {
		tracesURL := strings.Replace(endpoint, "/v1/metrics", "/v1/traces", 1)
		if insights, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(tracesURL)); err == nil {
			options = append(options, sdktrace.WithBatcher(insights, sdktrace.WithBatchTimeout(time.Second)))
		}
	}

	provider := sdktrace.NewTracerProvider(options...)

	otel.SetTracerProvider(provider)

	return nil
}
