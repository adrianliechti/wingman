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

func New(token string) (*Provider, error) {
	return &Provider{
		token: token,
	}, nil
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

	if !strings.EqualFold(token, p.token) {
		return ctx, errors.New("invalid token")
	}

	ctx = context.WithValue(ctx, auth.UserContextKey, token)

	return ctx, nil
}
