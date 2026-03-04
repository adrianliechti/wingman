package opa

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/policy"

	"github.com/open-policy-agent/opa/v1/rego"
)

type Provider struct {
	query rego.PreparedEvalQuery
}

type Option func(*Provider)

func New(path string, opts ...Option) (*Provider, error) {
	query, err := rego.New(
		rego.Query("data.wingman.allow"),
		rego.Load([]string{path}, nil),
	).PrepareForEval(context.Background())

	if err != nil {
		return nil, err
	}

	p := &Provider{
		query: query,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p, nil
}

func (p *Provider) Verify(ctx context.Context, resource policy.Resource, id string, action policy.Action) error {
	user, _ := ctx.Value(auth.UserContextKey).(string)
	email, _ := ctx.Value(auth.EmailContextKey).(string)

	evalInput := map[string]any{
		"resource": resource,
		"id":       id,
		"action":   action,

		"user":  user,
		"email": email,
	}

	results, err := p.query.Eval(ctx, rego.EvalInput(evalInput))

	if err != nil {
		return err
	}

	if len(results) == 0 {
		return policy.ErrAccessDenied
	}

	allowed, ok := results[0].Expressions[0].Value.(bool)

	if !ok || !allowed {
		return policy.ErrAccessDenied
	}

	return nil
}
