package anonymous

import (
	"context"
	"net/http"
)

type Provider struct {
}

func New() (*Provider, error) {
	p := &Provider{}

	return p, nil
}

func (p *Provider) Authenticate(ctx context.Context, r *http.Request) (context.Context, error) {
	return ctx, nil
}
