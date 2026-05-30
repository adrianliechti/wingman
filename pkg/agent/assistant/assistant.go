package assistant

import (
	"context"
	"errors"
	"iter"
	"slices"

	"github.com/adrianliechti/wingman/pkg/agent"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/template"
)

var _ agent.Agent = &Agent{}

type Agent struct {
	model string

	completer provider.Completer

	messages []provider.Message

	effort    provider.Effort
	verbosity provider.Verbosity

	temperature *float32
}

type Option func(*Agent)

func New(model string, options ...Option) (*Agent, error) {
	a := &Agent{
		model: model,
	}

	for _, option := range options {
		option(a)
	}

	if a.completer == nil {
		return nil, errors.New("missing completer provider")
	}

	return a, nil
}

func WithCompleter(completer provider.Completer) Option {
	return func(a *Agent) {
		a.completer = completer
	}
}

func WithMessages(messages ...provider.Message) Option {
	return func(a *Agent) {
		a.messages = messages
	}
}

func WithEffort(effort provider.Effort) Option {
	return func(a *Agent) {
		a.effort = effort
	}
}

func WithVerbosity(verbosity provider.Verbosity) Option {
	return func(a *Agent) {
		a.verbosity = verbosity
	}
}

func WithTemperature(temperature float32) Option {
	return func(a *Agent) {
		a.temperature = &temperature
	}
}

func (a *Agent) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		var opts provider.CompleteOptions
		if options != nil {
			opts = *options
		}

		if opts.OutputOptions == nil && a.verbosity != "" {
			opts.OutputOptions = &provider.OutputOptions{Verbosity: a.verbosity}
		}

		if opts.ReasoningOptions == nil && a.effort != "" {
			opts.ReasoningOptions = &provider.ReasoningOptions{Effort: a.effort}
		}

		if opts.Temperature == nil {
			opts.Temperature = a.temperature
		}

		if len(a.messages) > 0 {
			values, err := template.Messages(a.messages, nil)
			if err != nil {
				yield(nil, err)
				return
			}

			messages = slices.Concat(values, messages)
		}

		for completion, err := range a.completer.Complete(ctx, messages, &opts) {
			if err != nil {
				yield(nil, err)
				return
			}

			if a.model != "" && completion.Model != a.model {
				delta := *completion
				delta.Model = a.model

				if !yield(&delta, nil) {
					return
				}
				continue
			}

			if !yield(completion, nil) {
				return
			}
		}
	}
}
