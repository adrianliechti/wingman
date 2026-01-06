package roundrobin

import (
	"context"
	"iter"
	"math/rand"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
)

type Completer struct {
	completers []provider.Completer
}

func NewCompleter(routes ...router.Route) (provider.Completer, error) {
	completers := []provider.Completer{}

	for _, r := range routes {
		if r.Completer == nil {
			continue
		}

		completers = append(completers, r.Completer)
	}

	c := &Completer{
		completers: completers,
	}

	return c, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	index := rand.Intn(len(c.completers))
	provider := c.completers[index]

	return provider.Complete(ctx, messages, options)
}
