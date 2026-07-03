package opa

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/policy"

	"github.com/open-policy-agent/opa/v1/rego"
)

type File struct {
	query rego.PreparedEvalQuery
}

func NewFile(path string) (*File, error) {
	query, err := rego.New(
		rego.Query("data.wingman.allow"),
		rego.Load([]string{path}, nil),
	).PrepareForEval(context.Background())

	if err != nil {
		return nil, err
	}

	p := &File{
		query: query,
	}

	return p, nil
}

func (p *File) Verify(ctx context.Context, resource policy.Resource, id string, action policy.Action) error {
	results, err := p.query.Eval(ctx, rego.EvalInput(evalInput(ctx, resource, id, action)))

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
