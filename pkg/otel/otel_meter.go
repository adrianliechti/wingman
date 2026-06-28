package otel

import (
	"context"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
)

func setupMeter(ctx context.Context, resource *sdkresource.Resource) error {
	var err error
	var exporter sdkmetric.Exporter

	if strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")) == "grpc" || strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL")) == "grpc" {
		exporter, err = otlpmetricgrpc.New(ctx)
	} else {
		exporter, err = otlpmetrichttp.New(ctx)
	}

	if err != nil {
		return err
	}

	options := []sdkmetric.Option{
		sdkmetric.WithResource(resource),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(30*time.Second))),
	}

	// insights sums the datapoints it receives, so it must consume delta
	// temporality. The SDK does the cumulative→delta conversion correctly per
	// instrument (start times, resets, per-process state). This applies only to
	// the insights exporter; the primary OTLP exporter keeps the default
	// (cumulative) for backends like Prometheus.
	if endpoint := os.Getenv("INSIGHTS_ENDPOINT"); endpoint != "" {
		insights, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(endpoint),
			otlpmetrichttp.WithTemporalitySelector(deltaTemporality),
		)
		if err == nil {
			options = append(options, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(insights, sdkmetric.WithInterval(60*time.Second))))
		}
	}

	provider := sdkmetric.NewMeterProvider(options...)

	otel.SetMeterProvider(provider)

	return nil
}

// deltaTemporality is OTel's standard delta preference: delta for monotonic
// counters and histograms, cumulative for up/down counters (where delta is
// ill-defined). Summing delta datapoints over a window yields the true total.
func deltaTemporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	switch kind {
	case sdkmetric.InstrumentKindCounter,
		sdkmetric.InstrumentKindHistogram,
		sdkmetric.InstrumentKindObservableCounter:
		return metricdata.DeltaTemporality
	default:
		return metricdata.CumulativeTemporality
	}
}
