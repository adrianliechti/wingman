package google

import (
	"context"
	"errors"
	"io"
	"iter"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/toolsearch"

	"google.golang.org/genai/interactions/models/operations"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{model: model}
	for _, option := range options {
		option(cfg)
	}
	return &Completer{Config: cfg}, nil
}

// Complete uses stateless Interactions requests. Wingman owns the history, so
// requests disable storage and replay the full sequence of input/output steps.
func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		messages = provider.ResolveInstructions(messages)
		messages, options = provider.ResolveConfigurationUpdates(messages, options)
		messages, options = toolsearch.Inline(messages, options)

		request, err := convertInteraction(c.model, messages, options)
		if err != nil {
			yield(nil, provider.InvalidRequest(err))
			return
		}
		client, err := c.newClient(ctx)
		if err != nil {
			yield(nil, err)
			return
		}

		response, err := client.Interactions.Create(ctx, operations.CreateInteractionRequest{
			Body: operations.NewCreateInteractionRequestBody(*request),
		})
		if err != nil {
			yield(nil, convertError(err))
			return
		}

		state := newInteractionState(c.model, options)
		if response.Interaction != nil {
			result := response.Interaction
			state.setIdentity(value(result.ID), string(value(result.Model)))
			state.setTier(result.ServiceTier)
			for index, step := range result.Steps {
				delta, err := state.start(index, step)
				if err != nil {
					yield(nil, err)
					return
				}
				if delta != nil && !yield(delta, nil) {
					return
				}
				if delta, err := state.stop(index); err != nil || delta != nil {
					if !yield(delta, err) || err != nil {
						return
					}
				}
			}
			yield(state.finish(string(result.Status), result.Usage), nil)
			return
		}

		stream := response.InteractionSSEStreamEvent
		if stream == nil {
			yield(nil, errors.New("gemini: missing interaction stream"))
			return
		}
		defer stream.Close()
		for stream.Next() {
			delta, err := state.event(stream.Value().Data)
			if err != nil {
				if upstream, ok := errors.AsType[*provider.ProviderError](err); ok && response.HTTPMeta.Response != nil {
					upstream.RetryAfter = max(upstream.RetryAfter, parseRetryAfter(response.HTTPMeta.Response.Header))
				}
				yield(nil, err)
				return
			}
			if delta != nil && !yield(delta, nil) {
				return
			}
			if state.completed {
				return
			}
		}
		if err := stream.Err(); err != nil {
			yield(nil, convertError(err))
		} else if !state.terminal {
			// A clean transport EOF is not a completed model turn.
			yield(nil, io.ErrUnexpectedEOF)
		}
	}
}

func value[T any](p *T) (v T) {
	if p != nil {
		return *p
	}
	return v
}
