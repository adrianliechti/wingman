package otel

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
)

func TestShutdownFlushesTelemetry(t *testing.T) {
	for _, metricStatus := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(metricStatus), func(t *testing.T) {
			var exports sync.Map
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				exports.Store(r.URL.Path, body)
				w.Header().Set("Content-Type", "application/x-protobuf")
				if r.URL.Path == "/v1/metrics" {
					w.WriteHeader(metricStatus)
				}
			}))
			defer collector.Close()

			// Keep every exporter local, independent of the developer's environment.
			t.Setenv("INSIGHTS_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
			for _, signal := range []string{"traces", "metrics", "logs"} {
				prefix := "OTEL_EXPORTER_OTLP_" + strings.ToUpper(signal)
				t.Setenv(prefix+"_PROTOCOL", "http/protobuf")
				t.Setenv(prefix+"_ENDPOINT", collector.URL+"/v1/"+signal)
				t.Setenv(prefix+"_COMPRESSION", "none")
			}

			previousEnabled := EnableTelemetry
			previousTracer := otel.GetTracerProvider()
			previousMeter := otel.GetMeterProvider()
			previousLogger := otel.GetLoggerProvider()
			previousPropagator := otel.GetTextMapPropagator()
			previousSlog := slog.Default()
			previousTransport := http.DefaultTransport
			previousClientTransport := provider.DefaultClient.Transport
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = Shutdown(ctx)
				EnableTelemetry = previousEnabled
				otel.SetTracerProvider(previousTracer)
				otel.SetMeterProvider(previousMeter)
				otel.SetLoggerProvider(previousLogger)
				otel.SetTextMapPropagator(previousPropagator)
				slog.SetDefault(previousSlog)
				http.DefaultTransport = previousTransport
				provider.DefaultClient.Transport = previousClientTransport
			}()

			EnableTelemetry = true
			if err := Setup(); err != nil {
				t.Fatal(err)
			}

			ctx := t.Context()
			_, span := otel.Tracer("shutdown-test").Start(ctx, "pending-span")
			span.End()
			counter, err := otel.Meter("shutdown-test").Int64Counter("pending-counter")
			if err != nil {
				t.Fatal(err)
			}
			counter.Add(ctx, 1)
			slog.InfoContext(ctx, "pending-log")

			deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err = Shutdown(deadline)
			if wantError := metricStatus != http.StatusOK; (err != nil) != wantError {
				t.Fatalf("Shutdown() = %v, want error: %v", err, wantError)
			}

			// Logs must flush even if the metric exporter fails.
			for path, marker := range map[string]string{
				"/v1/traces":  "pending-span",
				"/v1/metrics": "pending-counter",
				"/v1/logs":    "pending-log",
			} {
				body, ok := exports.Load(path)
				if !ok || !bytes.Contains(body.([]byte), []byte(marker)) {
					t.Errorf("pending telemetry %q was not exported to %s", marker, path)
				}
			}
		})
	}
}
