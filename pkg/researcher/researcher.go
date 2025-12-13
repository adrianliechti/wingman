package researcher

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Provider interface {
	Research(ctx context.Context, instructions string, options *ResearchOptions) (*Result, error)
}

type Effort = provider.Effort
type Verbosity = provider.Verbosity

type ResearchOptions struct {
}

type Result struct {
	Content string
}
