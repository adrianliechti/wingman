package header

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"
)

type Provider struct {
	userHeader   string
	emailHeader  string
	tokenHeader  string
	groupsHeader string
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

	if p.tokenHeader == "" {
		p.tokenHeader = "X-Forwarded-Access-Token"
	}

	if p.groupsHeader == "" {
		p.groupsHeader = "X-Forwarded-Groups"
	}

	return p, nil
}

func (p *Provider) Authenticate(ctx context.Context, r *http.Request) (context.Context, error) {
	user := strings.TrimSpace(r.Header.Get(p.userHeader))
	email := strings.TrimSpace(r.Header.Get(p.emailHeader))
	token := strings.TrimSpace(r.Header.Get(p.tokenHeader))

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

	if token != "" {
		ctx = context.WithValue(ctx, auth.TokenContextKey, token)
	}

	if groups := parseGroups(r.Header.Get(p.groupsHeader)); len(groups) > 0 {
		ctx = context.WithValue(ctx, auth.GroupsContextKey, groups)
	}

	return ctx, nil
}

func parseGroups(value string) []string {
	var groups []string

	for _, g := range strings.Split(value, ",") {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}

	return groups
}
