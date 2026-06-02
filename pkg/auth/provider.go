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
	GroupsContextKey  contextKey = "auth.groups"
	SessionContextKey contextKey = "auth.session"
	TokenContextKey   contextKey = "auth.token"
)

type Provider interface {
	Authenticate(ctx context.Context, r *http.Request) (context.Context, error)
}
