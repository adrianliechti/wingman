package auth

import (
	"context"
	"net/http"
)

type contextKey string

const (
	UserContextKey    contextKey = "auth.user"
	EmailContextKey   contextKey = "auth.email"
	NameContextKey    contextKey = "auth.name"
	SessionContextKey contextKey = "auth.session"
)

type Provider interface {
	Authenticate(ctx context.Context, r *http.Request) (context.Context, error)
}
