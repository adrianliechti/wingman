package server

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/otel"
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
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		otel.Label(ctx, otel.EndUserAttrs(ctx)...)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
