package limiter

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/provider"

	"golang.org/x/time/rate"
)

type Completer interface {
	Limiter
	provider.Completer
}

type limitedCompleter struct {
	limiter  *rate.Limiter
	provider provider.Completer
}

func NewCompleter(l *rate.Limiter, p provider.Completer) Completer {
	return &limitedCompleter{
		limiter:  l,
		provider: p,
	}
}

func (p *limitedCompleter) limiterSetup() {
}

func (p *limitedCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if p.limiter != nil {
			p.limiter.Wait(ctx)
		}

		for completion, err := range p.provider.Complete(ctx, messages, options) {
			if !yield(completion, err) {
				return
			}
		}
	}
}
