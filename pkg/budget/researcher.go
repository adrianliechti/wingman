package budget

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/researcher"
)

type research struct {
	reporter
	researcher.Provider
}

// NewResearcher wraps a hosted research service that calls models outside
// Wingman. Agent researchers use Wingman models and are accounted there.
func NewResearcher(name, model string, p researcher.Provider, b Provider) researcher.Provider {
	return &research{reporter{b, name, model}, p}
}

func (p *research) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return func(yield func(*researcher.Result, error) bool) {
		r, err := p.start(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		defer p.finish(ctx, r)
		// Research services expose no token usage; report the operation with
		// unknown usage rather than inventing billable counts.
		for result, err := range p.Provider.Research(ctx, instructions, options) {
			if !yield(result, err) || err != nil {
				return
			}
		}
	}
}
