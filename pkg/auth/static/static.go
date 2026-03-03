package static

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"
)

type Provider struct {
	token string

	userHeader  string
	emailHeader string
}

type Option func(*Provider)

func New(token string, opts ...Option) (*Provider, error) {
	p := &Provider{
		token: token,
	}

	for _, opt := range opts {
		opt(p)
	}

	if p.userHeader == "" {
		p.userHeader = "X-Forwarded-User"
	}

	if p.emailHeader == "" {
		p.emailHeader = "X-Forwarded-Email"
	}

	return p, nil
}

func (p *Provider) Authenticate(ctx context.Context, r *http.Request) (context.Context, error) {
	if p.token == "" {
		return ctx, nil
	}

	header := r.Header.Get("Authorization")

	if header == "" {
		return ctx, errors.New("missing authorization header")
	}

	if !strings.HasPrefix(header, "Bearer ") {
		return ctx, errors.New("invalid authorization header")
	}

	token := strings.TrimPrefix(header, "Bearer ")

	if token != p.token {
		return ctx, errors.New("invalid token")
	}

	ctx = context.WithValue(ctx, auth.UserContextKey, token)

	user := strings.TrimSpace(r.Header.Get(p.userHeader))
	email := strings.TrimSpace(r.Header.Get(p.emailHeader))

	if email == "" && emailRegex.MatchString(user) {
		email = user
	}

	if user != "" {
		ctx = context.WithValue(ctx, auth.UserContextKey, user)
	}

	if email != "" {
		ctx = context.WithValue(ctx, auth.EmailContextKey, email)
	}

	return ctx, nil
}
