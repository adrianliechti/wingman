package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// handleRouteTag adds the matched chi route pattern to the otelhttp labeler as
// http.route, which otelhttp otherwise leaves empty. Register it below
// otelhttp.NewMiddleware (so the labeler is in context) and read the pattern
// after next returns (chi only fills RoutePattern() once the request is routed).
func handleRouteTag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)

		labeler, ok := otelhttp.LabelerFromContext(r.Context())
		if !ok {
			return
		}

		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			return
		}

		if pattern := rctx.RoutePattern(); pattern != "" {
			labeler.Add(semconv.HTTPRoute(pattern))
		}
	})
}
