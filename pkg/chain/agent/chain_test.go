package agent

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/stretchr/testify/require"
)

// mockCompleter implements provider.Completer for testing
type mockCompleter struct {
	// responses is a queue of responses to return on each Complete call
	responses [][]provider.Completion
	// callCount tracks how many times Complete was called
	callCount int
	// capturedMessages captures the messages passed to Complete
	capturedMessages [][]provider.Message
	// capturedOptions captures the options passed to Complete
	capturedOptions []*provider.CompleteOptions
	// err is an optional error to return
	err error
}

func (m *mockCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.capturedMessages = append(m.capturedMessages, messages)
		m.capturedOptions = append(m.capturedOptions, options)

		if m.err != nil {
			yield(nil, m.err)
			return
		}

		if m.callCount >= len(m.responses) {
			return
		}

		completions := m.responses[m.callCount]
		m.callCount++

		for i := range completions {
			if !yield(&completions[i], nil) {
				return
			}
		}
	}
}

// mockToolProvider implements tool.Provider for testing
type mockToolProvider struct {
	tools       []provider.Tool
	toolsErr    error
	executeFunc func(ctx context.Context, name string, params map[string]any) (any, error)
	// Track executions
	executeCalls []executeCall
}

type executeCall struct {
	Name   string
	Params map[string]any
}

func (m *mockToolProvider) Tools(ctx context.Context) ([]provider.Tool, error) {
	if m.toolsErr != nil {
		return nil, m.toolsErr
	}
	return m.tools, nil
}

func (m *mockToolProvider) Execute(ctx context.Context, name string, params map[string]any) (any, error) {
	m.executeCalls = append(m.executeCalls, executeCall{Name: name, Params: params})
	if m.executeFunc != nil {
		return m.executeFunc(ctx, name, params)
	}
	return map[string]any{"result": "ok"}, nil
}

// Helper to collect all completions from iterator
func collectCompletions(seq iter.Seq2[*provider.Completion, error]) ([]*provider.Completion, error) {
	var completions []*provider.Completion
	for completion, err := range seq {
		if err != nil {
			return completions, err
		}
		completions = append(completions, completion)
	}
	return completions, nil
}

// Helper to accumulate completions into final result
func accumulateCompletion(seq iter.Seq2[*provider.Completion, error]) (*provider.Completion, error) {
	acc := provider.CompletionAccumulator{}
	for completion, err := range seq {
		if err != nil {
			return nil, err
		}
		acc.Add(*completion)
	}
	return acc.Result(), nil
}

// =============================================================================
// TestNew - Chain creation tests
// =============================================================================

func TestNew(t *testing.T) {
	t.Run("missing completer returns error", func(t *testing.T) {
		_, err := New("test-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing completer")
	})

	t.Run("valid creation with completer", func(t *testing.T) {
		completer := &mockCompleter{}
		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)
		require.NotNil(t, chain)
		require.Equal(t, "test-model", chain.model)
	})

	t.Run("creation with all options", func(t *testing.T) {
		completer := &mockCompleter{}
		toolProvider := &mockToolProvider{}
		messages := []provider.Message{
			{Role: provider.MessageRoleSystem, Content: []provider.Content{{Text: "You are helpful"}}},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithMessages(messages...),
			WithTools(toolProvider),
			WithEffort(provider.EffortHigh),
			WithVerbosity(provider.VerbosityMedium),
			WithTemperature(0.7),
		)

		require.NoError(t, err)
		require.NotNil(t, chain)
		require.Equal(t, provider.EffortHigh, chain.effort)
		require.Equal(t, provider.VerbosityMedium, chain.verbosity)
		require.NotNil(t, chain.temperature)
		require.Equal(t, float32(0.7), *chain.temperature)
		require.Len(t, chain.messages, 1)
		require.Len(t, chain.tools, 1)
	})
}

// =============================================================================
// TestComplete_Basic - Basic completion pass-through
// =============================================================================

func TestComplete_Basic(t *testing.T) {
	t.Run("simple message pass-through", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						ID:    "resp-1",
						Model: "underlying-model",
						Message: &provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: []provider.Content{{Text: "Hello, how can I help?"}},
						},
					},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		messages := []provider.Message{
			{Role: provider.MessageRoleUser, Content: []provider.Content{{Text: "Hi"}}},
		}

		completions, err := collectCompletions(chain.Complete(context.Background(), messages, nil))
		require.NoError(t, err)
		require.Len(t, completions, 1)

		// Verify model name is overridden to chain's model
		require.Equal(t, "test-model", completions[0].Model)

		// Verify message content passed through
		require.NotNil(t, completions[0].Message)
		require.Equal(t, provider.MessageRoleAssistant, completions[0].Message.Role)
		require.Len(t, completions[0].Message.Content, 1)
		require.Equal(t, "Hello, how can I help?", completions[0].Message.Content[0].Text)

		// Verify completer received correct input
		require.Len(t, completer.capturedMessages, 1)
		require.Len(t, completer.capturedMessages[0], 1)
		require.Equal(t, "Hi", completer.capturedMessages[0][0].Content[0].Text)
	})

	t.Run("streaming multiple chunks", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "Hello"}}}},
					{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: " world"}}}},
					{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "!"}}}},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		messages := []provider.Message{
			{Role: provider.MessageRoleUser, Content: []provider.Content{{Text: "Hi"}}},
		}

		completions, err := collectCompletions(chain.Complete(context.Background(), messages, nil))
		require.NoError(t, err)
		require.Len(t, completions, 3)

		// Accumulate to get full text
		result, err := accumulateCompletion(chain.Complete(context.Background(), messages, nil))
		require.NoError(t, err)
		require.NotNil(t, result.Message)
	})

	t.Run("nil options handled", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant}}},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		_, err = collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)

		// Verify options were created (not nil)
		require.NotNil(t, completer.capturedOptions[0])
	})
}

// =============================================================================
// TestComplete_WithOptions - Effort, Verbosity, Temperature propagation
// =============================================================================

func TestComplete_WithOptions(t *testing.T) {
	t.Run("chain options propagate to completer", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant}}},
			},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithEffort(provider.EffortHigh),
			WithVerbosity(provider.VerbosityMedium),
			WithTemperature(0.8),
		)
		require.NoError(t, err)

		messages := []provider.Message{
			{Role: provider.MessageRoleUser, Content: []provider.Content{{Text: "Hi"}}},
		}

		_, err = collectCompletions(chain.Complete(context.Background(), messages, nil))
		require.NoError(t, err)

		// Verify options propagated
		opts := completer.capturedOptions[0]
		require.Equal(t, provider.EffortHigh, opts.Effort)
		require.Equal(t, provider.VerbosityMedium, opts.Verbosity)
		require.NotNil(t, opts.Temperature)
		require.Equal(t, float32(0.8), *opts.Temperature)
	})

	t.Run("call-time options override chain defaults", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant}}},
			},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithEffort(provider.EffortLow),
			WithVerbosity(provider.VerbosityLow),
		)
		require.NoError(t, err)

		callTemp := float32(0.5)
		callOpts := &provider.CompleteOptions{
			Effort:      provider.EffortHigh,
			Verbosity:   provider.VerbosityHigh,
			Temperature: &callTemp,
		}

		_, err = collectCompletions(chain.Complete(context.Background(), nil, callOpts))
		require.NoError(t, err)

		// Call-time options should take precedence
		opts := completer.capturedOptions[0]
		require.Equal(t, provider.EffortHigh, opts.Effort)
		require.Equal(t, provider.VerbosityHigh, opts.Verbosity)
		require.Equal(t, float32(0.5), *opts.Temperature)
	})

	t.Run("stop sequences and max tokens propagate", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant}}},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		maxTokens := 100
		callOpts := &provider.CompleteOptions{
			Stop:      []string{"STOP", "END"},
			MaxTokens: &maxTokens,
		}

		_, err = collectCompletions(chain.Complete(context.Background(), nil, callOpts))
		require.NoError(t, err)

		opts := completer.capturedOptions[0]
		require.Equal(t, []string{"STOP", "END"}, opts.Stop)
		require.NotNil(t, opts.MaxTokens)
		require.Equal(t, 100, *opts.MaxTokens)
	})
}

// =============================================================================
// TestComplete_WithMessages - System messages prepending
// =============================================================================

func TestComplete_WithMessages(t *testing.T) {
	t.Run("system messages prepended to input", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant}}},
			},
		}

		systemMessages := []provider.Message{
			{Role: provider.MessageRoleSystem, Content: []provider.Content{{Text: "You are a helpful assistant."}}},
			{Role: provider.MessageRoleSystem, Content: []provider.Content{{Text: "Be concise."}}},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithMessages(systemMessages...),
		)
		require.NoError(t, err)

		userMessages := []provider.Message{
			{Role: provider.MessageRoleUser, Content: []provider.Content{{Text: "Hello"}}},
		}

		_, err = collectCompletions(chain.Complete(context.Background(), userMessages, nil))
		require.NoError(t, err)

		// Verify message order: system messages first, then user messages
		captured := completer.capturedMessages[0]
		require.Len(t, captured, 3)
		require.Equal(t, provider.MessageRoleSystem, captured[0].Role)
		require.Equal(t, "You are a helpful assistant.", captured[0].Content[0].Text)
		require.Equal(t, provider.MessageRoleSystem, captured[1].Role)
		require.Equal(t, "Be concise.", captured[1].Content[0].Text)
		require.Equal(t, provider.MessageRoleUser, captured[2].Role)
		require.Equal(t, "Hello", captured[2].Content[0].Text)
	})
}

// =============================================================================
// TestComplete_WithTools - Server-side tool execution
// =============================================================================

func TestComplete_WithTools(t *testing.T) {
	t.Run("tool discovery and execution", func(t *testing.T) {
		// First response: model calls the tool
		// Second response: model provides final answer after tool result
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{
									ToolCall: &provider.ToolCall{
										ID:        "call-1",
										Name:      "get_weather",
										Arguments: `{"city": "London"}`,
									},
								},
							},
						},
					},
				},
				{
					{
						Message: &provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: []provider.Content{{Text: "The weather in London is sunny."}},
						},
					},
				},
			},
		}

		toolProvider := &mockToolProvider{
			tools: []provider.Tool{
				{
					Name:        "get_weather",
					Description: "Get weather for a city",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{"type": "string"},
						},
					},
				},
			},
			executeFunc: func(ctx context.Context, name string, params map[string]any) (any, error) {
				return map[string]any{"weather": "sunny", "temp": 20}, nil
			},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithTools(toolProvider),
		)
		require.NoError(t, err)

		messages := []provider.Message{
			{Role: provider.MessageRoleUser, Content: []provider.Content{{Text: "What's the weather in London?"}}},
		}

		_, err = collectCompletions(chain.Complete(context.Background(), messages, nil))
		require.NoError(t, err)

		// Verify tool was discovered and passed to completer
		require.Len(t, completer.capturedOptions[0].Tools, 1)
		require.Equal(t, "get_weather", completer.capturedOptions[0].Tools[0].Name)

		// Verify tool was executed with correct parameters
		require.Len(t, toolProvider.executeCalls, 1)
		require.Equal(t, "get_weather", toolProvider.executeCalls[0].Name)
		require.Equal(t, "London", toolProvider.executeCalls[0].Params["city"])

		// Verify completer was called twice (initial + after tool result)
		require.Equal(t, 2, completer.callCount)

		// Verify tool result was injected into messages for second call
		secondCallMessages := completer.capturedMessages[1]
		require.Len(t, secondCallMessages, 3) // user + assistant (tool call) + user (tool result)

		toolResultMsg := secondCallMessages[2]
		require.Equal(t, provider.MessageRoleUser, toolResultMsg.Role)
		require.NotNil(t, toolResultMsg.Content[0].ToolResult)
		require.Equal(t, "call-1", toolResultMsg.Content[0].ToolResult.ID)
	})

	t.Run("tools merged with options.Tools", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant}}},
			},
		}

		agentTool := &mockToolProvider{
			tools: []provider.Tool{
				{Name: "agent_tool", Description: "Agent tool"},
			},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithTools(agentTool),
		)
		require.NoError(t, err)

		optionsTool := provider.Tool{Name: "options_tool", Description: "Options tool"}
		opts := &provider.CompleteOptions{
			Tools: []provider.Tool{optionsTool},
		}

		_, err = collectCompletions(chain.Complete(context.Background(), nil, opts))
		require.NoError(t, err)

		// Both tools should be present
		tools := completer.capturedOptions[0].Tools
		require.Len(t, tools, 2)

		toolNames := make(map[string]bool)
		for _, t := range tools {
			toolNames[t.Name] = true
		}
		require.True(t, toolNames["agent_tool"])
		require.True(t, toolNames["options_tool"])
	})
}

// =============================================================================
// TestComplete_MultiToolLoop - Multi-turn tool calls
// =============================================================================

func TestComplete_MultiToolLoop(t *testing.T) {
	t.Run("multiple consecutive tool calls", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				// First: call tool A
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{ToolCall: &provider.ToolCall{ID: "call-a", Name: "tool_a", Arguments: `{"x": 1}`}},
							},
						},
					},
				},
				// Second: call tool B after getting result from A
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{ToolCall: &provider.ToolCall{ID: "call-b", Name: "tool_b", Arguments: `{"y": 2}`}},
							},
						},
					},
				},
				// Third: final response
				{
					{
						Message: &provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: []provider.Content{{Text: "Done with both tools"}},
						},
					},
				},
			},
		}

		toolProvider := &mockToolProvider{
			tools: []provider.Tool{
				{Name: "tool_a", Description: "Tool A"},
				{Name: "tool_b", Description: "Tool B"},
			},
			executeFunc: func(ctx context.Context, name string, params map[string]any) (any, error) {
				return map[string]any{"tool": name, "result": "success"}, nil
			},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithTools(toolProvider),
		)
		require.NoError(t, err)

		_, err = collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)

		// Verify completer called 3 times
		require.Equal(t, 3, completer.callCount)

		// Verify both tools executed
		require.Len(t, toolProvider.executeCalls, 2)
		require.Equal(t, "tool_a", toolProvider.executeCalls[0].Name)
		require.Equal(t, "tool_b", toolProvider.executeCalls[1].Name)
	})
}

// =============================================================================
// TestComplete_ToolFiltering - Agent tools filtered from stream
// =============================================================================

func TestComplete_ToolFiltering(t *testing.T) {
	t.Run("agent tool calls filtered from yield", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{Text: "Let me check..."},
								{ToolCall: &provider.ToolCall{ID: "call-1", Name: "agent_tool", Arguments: `{}`}},
							},
						},
					},
				},
				{
					{
						Message: &provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: []provider.Content{{Text: "Here's the result."}},
						},
					},
				},
			},
		}

		toolProvider := &mockToolProvider{
			tools: []provider.Tool{{Name: "agent_tool", Description: "Agent tool"}},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithTools(toolProvider),
		)
		require.NoError(t, err)

		completions, err := collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)

		// First completion should have text but no tool call (filtered)
		require.Len(t, completions[0].Message.Content, 1)
		require.Equal(t, "Let me check...", completions[0].Message.Content[0].Text)
		require.Nil(t, completions[0].Message.Content[0].ToolCall)
	})

	t.Run("non-agent tool calls NOT filtered", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{ToolCall: &provider.ToolCall{ID: "call-1", Name: "external_tool", Arguments: `{}`}},
							},
						},
					},
				},
			},
		}

		// No agent tools - the tool is passed via options
		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		opts := &provider.CompleteOptions{
			Tools: []provider.Tool{{Name: "external_tool", Description: "External"}},
		}

		completions, err := collectCompletions(chain.Complete(context.Background(), nil, opts))
		require.NoError(t, err)

		// Tool call should be present (not filtered)
		require.Len(t, completions, 1)
		require.NotNil(t, completions[0].Message.Content[0].ToolCall)
		require.Equal(t, "external_tool", completions[0].Message.Content[0].ToolCall.Name)
	})
}

// =============================================================================
// TestComplete_Errors - Error propagation
// =============================================================================

func TestComplete_Errors(t *testing.T) {
	t.Run("completer error propagates", func(t *testing.T) {
		completer := &mockCompleter{
			err: errors.New("completer failed"),
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		_, err = collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "completer failed")
	})

	t.Run("tool discovery error propagates", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{{Message: &provider.Message{Role: provider.MessageRoleAssistant}}},
			},
		}

		toolProvider := &mockToolProvider{
			toolsErr: errors.New("tools discovery failed"),
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithTools(toolProvider),
		)
		require.NoError(t, err)

		_, err = collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "tools discovery failed")
	})

	t.Run("tool execute error propagates", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{ToolCall: &provider.ToolCall{ID: "call-1", Name: "failing_tool", Arguments: `{}`}},
							},
						},
					},
				},
			},
		}

		toolProvider := &mockToolProvider{
			tools: []provider.Tool{{Name: "failing_tool", Description: "Fails"}},
			executeFunc: func(ctx context.Context, name string, params map[string]any) (any, error) {
				return nil, errors.New("tool execution failed")
			},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithTools(toolProvider),
		)
		require.NoError(t, err)

		_, err = collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "tool execution failed")
	})

	t.Run("invalid JSON arguments error propagates", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{ToolCall: &provider.ToolCall{ID: "call-1", Name: "tool", Arguments: `{invalid json}`}},
							},
						},
					},
				},
			},
		}

		toolProvider := &mockToolProvider{
			tools: []provider.Tool{{Name: "tool", Description: "Tool"}},
		}

		chain, err := New("test-model",
			WithCompleter(completer),
			WithTools(toolProvider),
		)
		require.NoError(t, err)

		_, err = collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.Error(t, err)
	})
}

// =============================================================================
// TestComplete_EarlyTermination - Yield returns false
// =============================================================================

func TestComplete_EarlyTermination(t *testing.T) {
	t.Run("stops when yield returns false", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "1"}}}},
					{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "2"}}}},
					{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "3"}}}},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		var collected []*provider.Completion
		for completion, err := range chain.Complete(context.Background(), nil, nil) {
			require.NoError(t, err)
			collected = append(collected, completion)
			if len(collected) >= 2 {
				break // Early termination
			}
		}

		require.Len(t, collected, 2)
	})
}

// =============================================================================
// TestComplete_CompletionID - ID consistency
// =============================================================================

func TestComplete_CompletionID(t *testing.T) {
	t.Run("all completions have same ID", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{ID: "chunk-1", Message: &provider.Message{Role: provider.MessageRoleAssistant}},
					{ID: "chunk-2", Message: &provider.Message{Role: provider.MessageRoleAssistant}},
					{ID: "chunk-3", Message: &provider.Message{Role: provider.MessageRoleAssistant}},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		completions, err := collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)
		require.Len(t, completions, 3)

		// All should have the same chain-generated ID
		firstID := completions[0].ID
		require.NotEmpty(t, firstID)
		for _, c := range completions {
			require.Equal(t, firstID, c.ID)
		}
	})
}

// =============================================================================
// TestComplete_Usage - Usage information
// =============================================================================

func TestComplete_Usage(t *testing.T) {
	t.Run("usage passed through", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{Role: provider.MessageRoleAssistant},
						Usage:   &provider.Usage{InputTokens: 10, OutputTokens: 20},
					},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		completions, err := collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)
		require.Len(t, completions, 1)

		require.NotNil(t, completions[0].Usage)
		require.Equal(t, 10, completions[0].Usage.InputTokens)
		require.Equal(t, 20, completions[0].Usage.OutputTokens)
	})
}

// =============================================================================
// TestComplete_Reasoning - Reasoning content pass-through
// =============================================================================

func TestComplete_Reasoning(t *testing.T) {
	t.Run("reasoning content passed through", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{
									Reasoning: &provider.Reasoning{
										ID:      "reasoning-1",
										Text:    "Let me think about this step by step...",
										Summary: "Thinking through the problem",
									},
								},
								{Text: "The answer is 42."},
							},
						},
					},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		completions, err := collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)
		require.Len(t, completions, 1)

		// Verify reasoning content passed through
		require.Len(t, completions[0].Message.Content, 2)

		reasoningContent := completions[0].Message.Content[0]
		require.NotNil(t, reasoningContent.Reasoning)
		require.Equal(t, "reasoning-1", reasoningContent.Reasoning.ID)
		require.Equal(t, "Let me think about this step by step...", reasoningContent.Reasoning.Text)
		require.Equal(t, "Thinking through the problem", reasoningContent.Reasoning.Summary)

		textContent := completions[0].Message.Content[1]
		require.Equal(t, "The answer is 42.", textContent.Text)
	})

	t.Run("reasoning streamed in chunks", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					// Chunk 1: reasoning starts
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{Reasoning: &provider.Reasoning{ID: "r1", Text: "First, "}},
							},
						},
					},
					// Chunk 2: reasoning continues
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{Reasoning: &provider.Reasoning{ID: "r1", Text: "I need to consider..."}},
							},
						},
					},
					// Chunk 3: text response
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{Text: "Here's my answer."},
							},
						},
					},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		completions, err := collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)
		require.Len(t, completions, 3)

		// First chunk has reasoning
		require.NotNil(t, completions[0].Message.Content[0].Reasoning)
		require.Equal(t, "First, ", completions[0].Message.Content[0].Reasoning.Text)

		// Second chunk has reasoning
		require.NotNil(t, completions[1].Message.Content[0].Reasoning)
		require.Equal(t, "I need to consider...", completions[1].Message.Content[0].Reasoning.Text)

		// Third chunk has text
		require.Equal(t, "Here's my answer.", completions[2].Message.Content[0].Text)
	})

	t.Run("reasoning with signature preserved", func(t *testing.T) {
		completer := &mockCompleter{
			responses: [][]provider.Completion{
				{
					{
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								{
									Reasoning: &provider.Reasoning{
										ID:        "reasoning-encrypted",
										Signature: "abc123signature",
									},
								},
								{Text: "Response after encrypted reasoning."},
							},
						},
					},
				},
			},
		}

		chain, err := New("test-model", WithCompleter(completer))
		require.NoError(t, err)

		completions, err := collectCompletions(chain.Complete(context.Background(), nil, nil))
		require.NoError(t, err)

		// Signature should be preserved for encrypted reasoning (e.g., Claude's thinking)
		require.NotNil(t, completions[0].Message.Content[0].Reasoning)
		require.Equal(t, "abc123signature", completions[0].Message.Content[0].Reasoning.Signature)
	})
}
