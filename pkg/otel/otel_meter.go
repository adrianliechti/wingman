package otel

import (
	"context"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
)

func setupMeter(ctx context.Context, resource *resource.Resource) error {
	var err error
	var exporter metric.Exporter

	if strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")) == "grpc" || strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL")) == "grpc" {
		exporter, err = otlpmetricgrpc.New(ctx)
	} else {
		exporter, err = otlpmetrichttp.New(ctx)
	}

	if err != nil {
		return err
	}

	options := []metric.Option{
		metric.WithResource(resource),
		metric.WithReader(metric.NewPeriodicReader(exporter, metric.WithInterval(30*time.Second))),
	}

	// insights sums the datapoints it receives, so it must consume delta
	// temporality. The SDK does the cumulative→delta conversion correctly per
	// instrument (start times, resets, per-process state). This applies only to
	// the insights exporter; the primary OTLP exporter keeps the default
	// (cumulative) for backends like Prometheus.
	if endpoint := os.Getenv("INSIGHTS_ENDPOINT"); endpoint != "" {
		insights, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(endpointWithDefaultPath(endpoint, "/v1/metrics")),
			otlpmetrichttp.WithTemporalitySelector(deltaTemporality),
		)
		if err == nil {
			options = append(options, metric.WithReader(metric.NewPeriodicReader(insights, metric.WithInterval(60*time.Second))))
		}
	}

	provider := metric.NewMeterProvider(options...)

	otel.SetMeterProvider(provider)

	return nil
}

func endpointWithDefaultPath(endpoint, defaultPath string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Path != "" {
		return endpoint
	}

	return u.JoinPath(defaultPath).String()
}

// deltaTemporality is OTel's standard delta preference: delta for monotonic
// counters and histograms, cumulative for up/down counters (where delta is
// ill-defined). Summing delta datapoints over a window yields the true total.
func deltaTemporality(kind metric.InstrumentKind) metricdata.Temporality {
	switch kind {
	case metric.InstrumentKindCounter,
		metric.InstrumentKindHistogram,
		metric.InstrumentKindObservableCounter:
		return metricdata.DeltaTemporality
	default:
		return metricdata.CumulativeTemporality
	}
}
