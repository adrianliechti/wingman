package rag

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/chain"
	"github.com/adrianliechti/wingman/pkg/index"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/template"
	"github.com/adrianliechti/wingman/pkg/text"
)

var _ chain.Provider = &Chain{}

type Chain struct {
	completer provider.Completer

	template *template.Template
	messages []provider.Message

	index index.Provider
	limit *int

	effort      provider.ReasoningEffort
	temperature *float32
}

type Option func(*Chain)

func New(options ...Option) (*Chain, error) {
	c := &Chain{
		template: template.MustTemplate(promptTemplate),
	}

	for _, option := range options {
		option(c)
	}

	if c.completer == nil {
		return nil, errors.New("missing completer provider")
	}

	if c.index == nil {
		return nil, errors.New("missing index provider")
	}

	return c, nil
}

func WithCompleter(completer provider.Completer) Option {
	return func(c *Chain) {
		c.completer = completer
	}
}

func WithTemplate(template *template.Template) Option {
	return func(c *Chain) {
		c.template = template
	}
}

func WithMessages(messages ...provider.Message) Option {
	return func(c *Chain) {
		c.messages = messages
	}
}

func WithIndex(index index.Provider) Option {
	return func(c *Chain) {
		c.index = index
	}
}

func WithLimit(limit int) Option {
	return func(c *Chain) {
		c.limit = &limit
	}
}

func WithEffort(effort provider.ReasoningEffort) Option {
	return func(c *Chain) {
		c.effort = effort
	}
}

func WithTemperature(temperature float32) Option {
	return func(c *Chain) {
		c.temperature = &temperature
	}
}

func (c *Chain) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	if options.Effort == "" {
		options.Effort = c.effort
	}

	if options.Temperature == nil {
		options.Temperature = c.temperature
	}

	message := messages[len(messages)-1]

	if message.Role != provider.MessageRoleUser {
		return nil, errors.New("last message must be from user")
	}

	query := strings.TrimSpace(message.Content.Text())

	results, err := c.index.Query(ctx, query, &index.QueryOptions{
		Limit: c.limit,
	})

	if err != nil {
		return nil, err
	}

	data := promptData{
		Input: query,
	}

	for _, r := range results {
		data.Results = append(data.Results, promptResult{
			Title:   r.Title,
			Source:  r.Source,
			Content: text.Normalize(r.Content),

			Metadata: r.Metadata,
		})
	}

	prompt, err := c.template.Execute(data)

	if err != nil {
		return nil, err
	}

	message = provider.UserMessage(prompt)
	messages[len(messages)-1] = message

	result, err := c.completer.Complete(ctx, messages, options)

	if err != nil {
		return nil, err
	}

	return result, nil
}
