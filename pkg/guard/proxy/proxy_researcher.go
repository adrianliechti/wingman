package proxy

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/researcher"
)

var _ researcher.Provider = (*guardedResearcher)(nil)

type guardedResearcher struct {
	guard      guard.Provider
	researcher researcher.Provider
}

func NewResearcher(g guard.Provider, p researcher.Provider) researcher.Provider {
	if g == nil {
		return p
	}

	return &guardedResearcher{
		guard:      g,
		researcher: p,
	}
}

func (p *guardedResearcher) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return func(yield func(*researcher.Result, error) bool) {
		if err := check(ctx, p.guard, instructions); err != nil {
			yield(nil, err)
			return
		}

		p.researcher.Research(ctx, instructions, options)(yield)
	}
}
