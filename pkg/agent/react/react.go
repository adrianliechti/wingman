package react

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"maps"
	"slices"

	"github.com/adrianliechti/wingman/pkg/agent"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/template"
	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/google/uuid"
)

var _ agent.Agent = &Agent{}

type ToolPhase int

const (
	ToolPhaseStart ToolPhase = iota + 1
	ToolPhaseResult
	ToolPhaseError
)

type ToolEvent struct {
	Phase ToolPhase

	CallID string
	Name   string

	Input map[string]any

	Result *provider.ToolResult
	Error  error
}

type ToolObserver func(ctx context.Context, event ToolEvent)

type Agent struct {
	model string

	completer provider.Completer

	tools    []tool.Provider
	messages []provider.Message

	effort    provider.Effort
	verbosity provider.Verbosity

	temperature *float32

	observer ToolObserver
}

type Option func(*Agent)

func New(model string, options ...Option) (*Agent, error) {
	c := &Agent{
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
	return func(c *Agent) {
		c.completer = completer
	}
}

func WithMessages(messages ...provider.Message) Option {
	return func(c *Agent) {
		c.messages = messages
	}
}

func WithTools(tool ...tool.Provider) Option {
	return func(c *Agent) {
		c.tools = tool
	}
}

func WithEffort(effort provider.Effort) Option {
	return func(c *Agent) {
		c.effort = effort
	}
}

func WithVerbosity(verbosity provider.Verbosity) Option {
	return func(c *Agent) {
		c.verbosity = verbosity
	}
}

func WithTemperature(temperature float32) Option {
	return func(c *Agent) {
		c.temperature = &temperature
	}
}

func WithToolObserver(observer ToolObserver) Option {
	return func(c *Agent) {
		c.observer = observer
	}
}

func (c *Agent) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		// Work on a local copy so the caller's CompleteOptions is never mutated.
		var opts provider.CompleteOptions
		if options != nil {
			opts = *options
		}

		if opts.OutputOptions == nil && c.verbosity != "" {
			opts.OutputOptions = &provider.OutputOptions{
				Verbosity: c.verbosity,
			}
		}

		if opts.ReasoningOptions == nil && c.effort != "" {
			opts.ReasoningOptions = &provider.ReasoningOptions{
				Effort: c.effort,
			}
		}

		if opts.Temperature == nil {
			opts.Temperature = c.temperature
		}

		if len(c.messages) > 0 {
			values, err := template.Messages(c.messages, nil)

			if err != nil {
				yield(nil, err)
				return
			}

			messages = slices.Concat(values, messages)
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

		for _, t := range opts.Tools {
			inputTools[t.Name] = t
		}

		inputToolOptions := mergeToolOptions(opts.ToolOptions, slices.Collect(maps.Keys(agentTools)))

		inputOptions := &provider.CompleteOptions{
			Stop:        opts.Stop,
			Tools:       slices.Collect(maps.Values(inputTools)),
			ToolOptions: inputToolOptions,

			OutputOptions:    opts.OutputOptions,
			ReasoningOptions: opts.ReasoningOptions,

			MaxTokens:   opts.MaxTokens,
			Temperature: opts.Temperature,

			Schema: opts.Schema,
		}

		accID := uuid.New().String()

		toolNamesByID := map[string]string{}
		var lastToolCallID string

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
						if cnt.ToolCall != nil {
							id := cnt.ToolCall.ID
							name := cnt.ToolCall.Name

							// Streaming providers vary in what they put on argument-delta
							// chunks: some include the ID, some include nothing. Track the
							// last seen ID so deltas can be attributed to the right call.
							if id != "" {
								lastToolCallID = id
								if name != "" {
									toolNamesByID[id] = name
								}
							} else if name == "" {
								id = lastToolCallID
							}

							if name == "" {
								name = toolNamesByID[id]
							}

							if _, found := agentTools[name]; found {
								continue
							}
						}

						message.Content = append(message.Content, cnt)
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

			var hasAgentCall, hasCallerCall bool

			for _, cnt := range completion.Message.Content {
				if cnt.ToolCall == nil {
					continue
				}

				if _, isAgent := agentTools[cnt.ToolCall.Name]; isAgent {
					hasAgentCall = true
				} else {
					hasCallerCall = true
				}
			}

			// Agent tools are executed in-loop; caller tools are surfaced through
			// the stream for the caller to handle. A single assistant turn cannot
			// span both — the chain would either loop without the caller's result
			// (provider 400) or yield without the agent result (lost work).
			if hasAgentCall && hasCallerCall {
				yield(nil, errors.New("agent: model returned both agent-handled and caller-handled tool calls in one turn"))
				return
			}

			if !hasAgentCall {
				return
			}

			input = append(input, *completion.Message)

			for _, cnt := range completion.Message.Content {
				if cnt.ToolCall == nil {
					continue
				}

				t := agentTools[cnt.ToolCall.Name]

				var params map[string]any

				if err := json.Unmarshal([]byte(cnt.ToolCall.Arguments), &params); err != nil {
					yield(nil, err)
					return
				}

				if c.observer != nil {
					c.observer(ctx, ToolEvent{
						Phase:  ToolPhaseStart,
						CallID: cnt.ToolCall.ID,
						Name:   cnt.ToolCall.Name,
						Input:  params,
					})
				}

				result, err := t.Execute(ctx, cnt.ToolCall.Name, params)

				if err != nil {
					if c.observer != nil {
						c.observer(ctx, ToolEvent{
							Phase:  ToolPhaseError,
							CallID: cnt.ToolCall.ID,
							Name:   cnt.ToolCall.Name,
							Input:  params,
							Error:  err,
						})
					}

					input = append(input, provider.Message{
						Role: provider.MessageRoleUser,
						Content: []provider.Content{
							provider.ToolResultContent(provider.ToolResult{
								ID:    cnt.ToolCall.ID,
								Parts: []provider.Part{{Text: "Error: " + err.Error()}},
							}),
						},
					})

					continue
				}

				toolResult, err := renderToolResult(t, cnt.ToolCall.ID, cnt.ToolCall.Name, result)

				if err != nil {
					yield(nil, err)
					return
				}

				if c.observer != nil {
					c.observer(ctx, ToolEvent{
						Phase:  ToolPhaseResult,
						CallID: cnt.ToolCall.ID,
						Name:   cnt.ToolCall.Name,
						Input:  params,
						Result: &toolResult,
					})
				}

				input = append(input, provider.Message{
					Role: provider.MessageRoleUser,

					Content: []provider.Content{
						provider.ToolResultContent(toolResult),
					},
				})
			}
		}
	}
}

// mergeToolOptions combines user-specified ToolOptions with the agent's internal tool names.
//
// Agent tools are transparent to the user — they are executed by the chain loop and
// never surfaced in the streamed output. Therefore:
//
//   - If no agent tools are registered, the user's options pass through unchanged.
//   - DisableParallelToolCalls is forced on. Agent and caller tools cannot share a
//     single assistant turn (see chain loop); serializing tool calls prevents the
//     model from producing a turn the chain can't reconcile.
//   - If the user specified ToolChoiceNone, we switch to Auto so agent tools can still
//     fire, but restrict Allowed to only agent tool names so user tools remain uncallable.
//   - If the user restricted the allowed list, we union it with agent tool names so the
//     model can still invoke agent tools while respecting the user's restrictions.
func mergeToolOptions(opts *provider.ToolOptions, agentToolNames []string) *provider.ToolOptions {
	if len(agentToolNames) == 0 {
		return opts
	}

	var merged provider.ToolOptions
	if opts != nil {
		merged = *opts
	}

	merged.DisableParallelToolCalls = true

	switch merged.Choice {
	case provider.ToolChoiceNone:
		// User wants no user-visible tool calls. Allow agent tools only.
		merged.Choice = provider.ToolChoiceAuto
		merged.Allowed = slices.Clone(agentToolNames)

	case "":
		merged.Choice = provider.ToolChoiceAuto

	default:
		// If the user restricted to specific tools, also allow all agent tools.
		if len(merged.Allowed) > 0 {
			combined := slices.Clone(merged.Allowed)
			for _, name := range agentToolNames {
				if !slices.Contains(combined, name) {
					combined = append(combined, name)
				}
			}
			merged.Allowed = combined
		}
	}

	return &merged
}

func renderToolResult(t tool.Provider, id, name string, value any) (provider.ToolResult, error) {
	if r, ok := t.(tool.Resulter); ok {
		result := r.Result(name, value)
		if result.ID == "" {
			result.ID = id
		}
		return result, nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return provider.ToolResult{}, err
	}

	return provider.ToolResult{
		ID:    id,
		Parts: []provider.Part{{Text: string(data)}},
	}, nil
}
