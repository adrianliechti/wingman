package researcher

import (
	"context"
)

type Provider interface {
	Research(ctx context.Context, instructions string, options *ResearchOptions) (*Result, error)
}

type ResearchOptions struct {
}

type Result struct {
	Content string
}
