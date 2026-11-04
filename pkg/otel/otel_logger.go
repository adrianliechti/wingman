package otel

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

func setupLogger(ctx context.Context, resource *resource.Resource) error {
	var err error
	var exporter log.Exporter

	if strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")) == "grpc" || strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL")) == "grpc" {
		exporter, err = otlploggrpc.New(ctx)
	} else {
		exporter, err = otlploghttp.New(ctx)
	}

	if err != nil {
		return err
	}

	provider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exporter)),
		log.WithResource(resource),
	)

	otel.SetLoggerProvider(provider)

	logger := otelslog.NewLogger("", otelslog.WithLoggerProvider(provider))
	slog.SetDefault(logger)

	return nil
}
