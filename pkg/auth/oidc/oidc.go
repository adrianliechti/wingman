package oidc

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"

	"github.com/coreos/go-oidc/v3/oidc"
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

func (p *Provider) Authenticate(ctx context.Context, r *http.Request) (context.Context, error) {
	header := r.Header.Get("Authorization")

	if header == "" {
		header = r.Header.Get("X-Forwarded-Access-Token")
	}

	if header == "" {
		return ctx, errors.New("missing authorization header")
	}

	token, ok := strings.CutPrefix(header, "Bearer ")

	if !ok {
		return ctx, errors.New("invalid authorization header")
	}

	idtoken, err := p.verifier.Verify(ctx, token)

	if err != nil {
		return ctx, err
	}

	ctx = context.WithValue(ctx, auth.TokenContextKey, token)

	var claims struct {
		// OAuth 2.0 / JWT (RFC 7519)
		Subject string `json:"sub"`

		// OpenID Connect
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		AZP               string `json:"azp"`

		// Microsoft Entra
		ObjectID string   `json:"oid"`
		UPN      string   `json:"upn"`
		AppID    string   `json:"appid"`
		Groups   []string `json:"groups"`
	}

	if err := idtoken.Claims(&claims); err == nil {
		if user := firstNonEmpty(claims.ObjectID, claims.Subject); user != "" {
			ctx = context.WithValue(ctx, auth.UserContextKey, user)
		}

		email := claims.Email

		if email == "" {
			if v := firstNonEmpty(claims.PreferredUsername, claims.UPN); emailRegex.MatchString(v) {
				email = v
			}
		}

		if email != "" {
			ctx = context.WithValue(ctx, auth.EmailContextKey, email)
		}

		if name := firstNonEmpty(claims.Name, claims.PreferredUsername, claims.UPN, claims.AZP, claims.AppID); name != "" {
			ctx = context.WithValue(ctx, auth.NameContextKey, name)
		}

		if peer := firstNonEmpty(claims.AZP, claims.AppID); peer != "" {
			ctx = context.WithValue(ctx, auth.PeerContextKey, peer)
		}

		if len(claims.Groups) > 0 {
			ctx = context.WithValue(ctx, auth.GroupsContextKey, claims.Groups)
		}
	}

	return ctx, nil
}
