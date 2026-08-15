package server

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/request"
)

func handleRequestMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadata := request.FromHeader(r.Header)
		ctx := request.WithContext(r.Context(), metadata)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
