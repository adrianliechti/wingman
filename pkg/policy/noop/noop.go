package noop

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/policy"
)

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Verify(ctx context.Context, resource policy.Resource, id string, action policy.Action) error {
	return nil
}
