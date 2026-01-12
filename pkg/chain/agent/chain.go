package agent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"maps"
	"slices"

	"github.com/adrianliechti/wingman/pkg/chain"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/template"
	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/google/uuid"
)

var _ chain.Provider = &Chain{}

type Chain struct {
	model string

	completer provider.Completer

	tools    []tool.Provider
	messages []provider.Message

	effort    provider.Effort
	verbosity provider.Verbosity

	temperature *float32
}

type Option func(*Chain)

func New(model string, options ...Option) (*Chain, error) {
	c := &Chain{
		model: model,
	}

	for _, option := range options {
		option(c)
	}

	if c.completer == nil {
		return nil, errors.New("missing completer provider")
	}

	return c, nil
}

func WithCompleter(completer provider.Completer) Option {
	return func(c *Chain) {
		c.completer = completer
	}
}

func WithMessages(messages ...provider.Message) Option {
	return func(c *Chain) {
		c.messages = messages
	}
}

func WithTools(tool ...tool.Provider) Option {
	return func(c *Chain) {
		c.tools = tool
	}
}

func WithEffort(effort provider.Effort) Option {
	return func(c *Chain) {
		c.effort = effort
	}
}

func WithVerbosity(verbosity provider.Verbosity) Option {
	return func(c *Chain) {
		c.verbosity = verbosity
	}
}

func WithTemperature(temperature float32) Option {
	return func(c *Chain) {
		c.temperature = &temperature
	}
}

func (c *Chain) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		if options.Effort == "" {
			options.Effort = c.effort
		}

		if options.Temperature == nil {
			options.Temperature = c.temperature
		}

		if len(c.messages) > 0 {
			values, err := template.Messages(c.messages, nil)

			if err != nil {
				yield(nil, err)
				return
			}

			messages = slices.Concat(values, messages)
		}

		var contextFiles []provider.File

		for _, m := range messages {
			var files []provider.File

			for _, c := range m.Content {
				if c.File != nil {
					files = append(files, *c.File)
				}
			}

			contextFiles = files
		}

		if len(contextFiles) > 0 {
			ctx = tool.WithFiles(ctx, contextFiles)
		}

		input := slices.Clone(messages)

		agentTools := make(map[string]tool.Provider)
		inputTools := make(map[string]provider.Tool)

		for _, p := range c.tools {
			tools, err := p.Tools(ctx)

			if err != nil {
				yield(nil, err)
				return
			}

			for _, tool := range tools {
				agentTools[tool.Name] = p
				inputTools[tool.Name] = tool
			}
		}

		for _, t := range options.Tools {
			inputTools[t.Name] = t
		}

		inputOptions := &provider.CompleteOptions{
			Effort: options.Effort,

			Stop:  options.Stop,
			Tools: slices.Collect(maps.Values(inputTools)),

			MaxTokens:   options.MaxTokens,
			Temperature: options.Temperature,

			Schema: options.Schema,
		}

		accID := uuid.New().String()

		var lastToolID string
		var lastToolName string

		for {
			acc := provider.CompletionAccumulator{}

			for completion, err := range c.completer.Complete(ctx, input, inputOptions) {
				if err != nil {
					yield(nil, err)
					return
				}

				acc.Add(*completion)

				delta := &provider.Completion{
					ID:    accID,
					Model: c.model,

					Usage: completion.Usage,
				}

				if completion.Message != nil {
					message := &provider.Message{
						Role: completion.Message.Role,
					}

					for _, cnt := range completion.Message.Content {
						if cnt.Text != "" {
							message.Content = append(message.Content, provider.TextContent(cnt.Text))
						}

						if cnt.ToolCall != nil {
							if cnt.ToolCall.ID != "" {
								lastToolID = cnt.ToolCall.ID
							}

							if cnt.ToolCall.Name != "" {
								lastToolName = cnt.ToolCall.Name
							}

							if lastToolName != "" {
								if _, found := agentTools[lastToolName]; found {
									continue
								}

								message.Content = append(message.Content, provider.ToolCallContent(provider.ToolCall{
									ID:   lastToolID,
									Name: lastToolName,

									Arguments: cnt.ToolCall.Arguments,
								}))
							}
						}
					}

					delta.Message = message
				}

				if !yield(delta, nil) {
					return
				}
			}

			completion := acc.Result()

			completion.ID = accID
			completion.Model = c.model

			if completion.Message == nil {
				return
			}

			var loop bool

			input = append(input, *completion.Message)

			for _, cnt := range completion.Message.Content {
				if cnt.ToolCall == nil {
					continue
				}

				t, found := agentTools[cnt.ToolCall.Name]

				if !found {
					continue
				}

				var params map[string]any

				if err := json.Unmarshal([]byte(cnt.ToolCall.Arguments), &params); err != nil {
					yield(nil, err)
					return
				}

				result, err := t.Execute(ctx, cnt.ToolCall.Name, params)

				if err != nil {
					yield(nil, err)
					return
				}

				data, err := json.Marshal(result)

				if err != nil {
					yield(nil, err)
					return
				}

				input = append(input, provider.Message{
					Role: provider.MessageRoleUser,

					Content: []provider.Content{
						provider.ToolResultContent(provider.ToolResult{
							ID:   cnt.ToolCall.ID,
							Data: string(data),
						}),
					},
				})

				loop = true
			}

			if !loop {
				return
			}
		}
	}
}
