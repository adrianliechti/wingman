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
}

type Option func(*Provider)

func New(token string, opts ...Option) (*Provider, error) {
	p := &Provider{
		token: token,
	}

	for _, opt := range opts {
		opt(p)
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

	token, ok := strings.CutPrefix(header, "Bearer ")

	if !ok {
		return ctx, errors.New("invalid authorization header")
	}

	if token != p.token {
		return ctx, errors.New("invalid token")
	}

	ctx = context.WithValue(ctx, auth.UserContextKey, token)

	return ctx, nil
}
