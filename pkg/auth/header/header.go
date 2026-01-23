package header

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

type Provider struct {
	userHeader  string
	emailHeader string
}

type Option func(*Provider)

func New(opts ...Option) (*Provider, error) {
	p := &Provider{}

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
	user := strings.TrimSpace(r.Header.Get(p.userHeader))
	email := strings.TrimSpace(r.Header.Get(p.emailHeader))

	if user == "" && email == "" {
		return ctx, errors.New("no user information found in headers")
	}

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
