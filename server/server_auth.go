package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/budget"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/server/web"
)

func (s *Server) handleAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var authorized = len(s.Authorizers) == 0

		for _, a := range s.Authorizers {
			if authCtx, err := a.Authenticate(ctx, r); err == nil {
				ctx = authCtx
				authorized = true
				break
			}
		}

		if !authorized {
			writeUnauthorized(w, r)
			return
		}

		if s.Budget != nil {
			ctx = budget.WithAdmission(ctx)
		}

		otel.Label(ctx, otel.EndUserAttrs(ctx)...)
		otel.SetEndUserSpan(ctx)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeUnauthorized answers a failed authentication with the error body of
// the API the request was made against.
func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	const message = "invalid or missing credentials"

	var body any
	if web.IsTavily(r) || web.IsExa(r) {
		web.WriteError(w, r, http.StatusUnauthorized, message)
		return
	}

	switch path := r.URL.Path; {
	case strings.HasPrefix(path, "/v1beta/"):
		body = map[string]any{
			"error": map[string]any{"code": http.StatusUnauthorized, "message": message, "status": "UNAUTHENTICATED"},
		}

	case strings.HasPrefix(path, "/v1/messages"):
		body = map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "authentication_error", "message": message},
		}

	default:
		body = map[string]any{
			"error": map[string]any{"type": "invalid_request_error", "code": "invalid_api_key", "message": message},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)

	json.NewEncoder(w).Encode(body)
}
