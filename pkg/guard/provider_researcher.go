package guard

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/researcher"
)

var _ researcher.Provider = (*guardedResearcher)(nil)

type guardedResearcher struct {
	guard      Provider
	researcher researcher.Provider
}

func NewResearcher(g Provider, next researcher.Provider) researcher.Provider {
	if g == nil {
		return next
	}
	return &guardedResearcher{guard: g, researcher: next}
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
