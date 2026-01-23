package oidc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Provider struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
}

func New(issuer, audience string) (*Provider, error) {
	cfg := &oidc.Config{
		ClientID: audience,
	}

	provider, err := oidc.NewProvider(context.Background(), issuer)

	if err != nil {
		return nil, err
	}

	verifier := provider.Verifier(cfg)

	return &Provider{
		provider: provider,
		verifier: verifier,
	}, nil
}

func (p *Provider) Authenticate(ctx context.Context, r *http.Request) (context.Context, error) {
	header := r.Header.Get("Authorization")

	if header == "" {
		return ctx, errors.New("missing authorization header")
	}

	if !strings.HasPrefix(header, "Bearer ") {
		return ctx, errors.New("invalid authorization header")
	}

	token := strings.TrimPrefix(header, "Bearer ")

	idtoken, err := p.verifier.Verify(ctx, token)

	if err != nil {
		return ctx, err
	}

	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
	}

	if err := idtoken.Claims(&claims); err == nil {
		if claims.Subject != "" {
			ctx = context.WithValue(ctx, auth.UserContextKey, claims.Subject)
		}

		if claims.Email != "" {
			ctx = context.WithValue(ctx, auth.EmailContextKey, claims.Email)
		}
	}

	return ctx, nil
}
