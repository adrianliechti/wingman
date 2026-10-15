package researcher

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/guard"
)

var _ Provider = (*guardedResearcher)(nil)

type guardedResearcher struct {
	guard      guard.Provider
	researcher Provider
}

// NewGuard checks input before forwarding requests to the provider.
func NewGuard(g guard.Provider, next Provider) Provider {
	if g == nil {
		return next
	}
	return &guardedResearcher{guard: g, researcher: next}
}

func (p *guardedResearcher) Research(ctx context.Context, instructions string, options *ResearchOptions) iter.Seq2[*Result, error] {
	return func(yield func(*Result, error) bool) {
		if err := guard.Check(ctx, p.guard, instructions); err != nil {
			yield(nil, err)
			return
		}

		p.researcher.Research(ctx, instructions, options)(yield)
	}
}
