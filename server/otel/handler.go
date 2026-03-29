package otel

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	logs    http.Handler
	traces  http.Handler
	metrics http.Handler
}

func New() *Handler {
	base := strings.TrimRight(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), "/")

	logsURL := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")

	if logsURL == "" && base != "" {
		logsURL = base + "/v1/logs"
	}

	tracesURL := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")

	if tracesURL == "" && base != "" {
		tracesURL = base + "/v1/traces"
	}

	metricsURL := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")

	if metricsURL == "" && base != "" {
		metricsURL = base + "/v1/metrics"
	}

	return &Handler{
		logs:    newHandler(logsURL),
		traces:  newHandler(tracesURL),
		metrics: newHandler(metricsURL),
	}
}

func (h *Handler) Attach(r chi.Router) {
	r.Post("/otel/logs", h.logs.ServeHTTP)
	r.Post("/otel/traces", h.traces.ServeHTTP)
	r.Post("/otel/metrics", h.metrics.ServeHTTP)
}

func newHandler(endpoint string) http.Handler {
	if endpoint == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}"))
		})
	}

	target, _ := url.Parse(endpoint)

	return &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetXForwarded()
			req.Out.URL.Scheme = target.Scheme
			req.Out.URL.Host = target.Host
			req.Out.URL.Path = target.Path
			req.Out.URL.RawQuery = target.RawQuery
			req.Out.Host = target.Host
		},
	}
}
