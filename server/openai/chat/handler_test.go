package chat_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/stretchr/testify/require"
)

const (
	testBaseURL = "http://localhost:8080/v1/"
	testModel   = "gpt-5.2"
	testTimeout = 60 * time.Second
)

func newTestClient() openai.Client {
	return openai.NewClient(
		option.WithBaseURL(testBaseURL),
		option.WithAPIKey("test-key"),
	)
}

func checkServer(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testBaseURL+"models", nil)
	if err != nil {
		t.Skipf("skipping test: failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("skipping test: server not available at %s: %v", testBaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Skipf("skipping test: server returned status %d", resp.StatusCode)
	}
}

func TestChatCompletion(t *testing.T) {
	checkServer(t)
	t.Parallel()

	client := newTestClient()

	tests := []struct {
		name     string
		messages []openai.ChatCompletionMessageParamUnion
	}{
		{
			name: "single user message",
			messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say 'hello' and nothing else."),
			},
		},
		{
			name: "with system prompt",
			messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage("You are a helpful assistant that responds concisely."),
				openai.UserMessage("What is 2+2? Reply with just the number."),
			},
		},
		{
			name: "multi-turn conversation",
			messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage("You are a helpful assistant."),
				openai.UserMessage("My name is Alice."),
				openai.AssistantMessage("Nice to meet you, Alice!"),
				openai.UserMessage("What is my name? Reply with just the name."),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("non-streaming", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
					Model:    testModel,
					Messages: tt.messages,
				})
				require.NoError(t, err)
				require.NotNil(t, completion)
				require.NotEmpty(t, completion.Choices)
				require.NotEmpty(t, completion.Choices[0].Message.Content)
				require.NotEmpty(t, completion.Choices[0].FinishReason)
			})

			t.Run("streaming", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
					Model:    testModel,
					Messages: tt.messages,
				})

				var content string
				var finishReason string

				for stream.Next() {
					chunk := stream.Current()
					if len(chunk.Choices) > 0 {
						content += chunk.Choices[0].Delta.Content
						if chunk.Choices[0].FinishReason != "" {
							finishReason = string(chunk.Choices[0].FinishReason)
						}
					}
				}

				require.NoError(t, stream.Err())
				require.NotEmpty(t, content)
				require.NotEmpty(t, finishReason)
			})
		})
	}
}

func TestChatCompletionToolCalling(t *testing.T) {
	checkServer(t)
	t.Parallel()

	client := newTestClient()

	weatherTool := openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name:        "get_weather",
		Description: openai.String("Get the current weather for a location"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "The city and country, e.g. 'London, UK'",
				},
			},
			"required": []string{"location"},
		},
	})

	calculatorTool := openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name:        "calculate",
		Description: openai.String("Perform a mathematical calculation"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "The mathematical expression to evaluate",
				},
			},
			"required": []string{"expression"},
		},
	})

	tests := []struct {
		name     string
		messages []openai.ChatCompletionMessageParamUnion
		tools    []openai.ChatCompletionToolUnionParam
	}{
		{
			name: "single tool",
			messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("What's the weather in London?"),
			},
			tools: []openai.ChatCompletionToolUnionParam{weatherTool},
		},
		{
			name: "multiple tools",
			messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("What's the weather in Paris?"),
			},
			tools: []openai.ChatCompletionToolUnionParam{weatherTool, calculatorTool},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("non-streaming", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
					Model:    testModel,
					Messages: tt.messages,
					Tools:    tt.tools,
				})
				require.NoError(t, err)
				require.NotNil(t, completion)
				require.NotEmpty(t, completion.Choices)

				choice := completion.Choices[0]
				require.NotEmpty(t, choice.FinishReason)

				// Either we get a tool call or a regular message
				if choice.FinishReason == "tool_calls" {
					require.NotEmpty(t, choice.Message.ToolCalls)
					toolCall := choice.Message.ToolCalls[0]
					require.NotEmpty(t, toolCall.ID)
					require.NotEmpty(t, toolCall.Function.Name)
					require.NotEmpty(t, toolCall.Function.Arguments)
				}
			})

			t.Run("streaming", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
					Model:    testModel,
					Messages: tt.messages,
					Tools:    tt.tools,
				})

				acc := openai.ChatCompletionAccumulator{}

				for stream.Next() {
					chunk := stream.Current()
					acc.AddChunk(chunk)
				}

				require.NoError(t, stream.Err())
				require.NotEmpty(t, acc.Choices)

				choice := acc.Choices[0]
				require.NotEmpty(t, choice.FinishReason)

				if choice.FinishReason == "tool_calls" {
					require.NotEmpty(t, choice.Message.ToolCalls)
					toolCall := choice.Message.ToolCalls[0]
					require.NotEmpty(t, toolCall.ID)
					require.NotEmpty(t, toolCall.Function.Name)
					require.NotEmpty(t, toolCall.Function.Arguments)
				}
			})
		})
	}
}

func TestChatCompletionToolResult(t *testing.T) {
	checkServer(t)
	t.Parallel()

	client := newTestClient()

	// Simulate a completed tool call flow by building the assistant message with tool calls
	assistantMsg := openai.ChatCompletionAssistantMessageParam{
		ToolCalls: []openai.ChatCompletionMessageToolCallUnionParam{
			{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: "call_123",
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      "get_weather",
						Arguments: `{"location": "London, UK"}`,
					},
				},
			},
		},
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("What's the weather in London?"),
		{OfAssistant: &assistantMsg},
		openai.ToolMessage("Sunny, 22°C", "call_123"),
	}

	t.Run("non-streaming", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    testModel,
			Messages: messages,
		})
		require.NoError(t, err)
		require.NotNil(t, completion)
		require.NotEmpty(t, completion.Choices)
		require.NotEmpty(t, completion.Choices[0].Message.Content)
	})

	t.Run("streaming", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model:    testModel,
			Messages: messages,
		})

		var content string
		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) > 0 {
				content += chunk.Choices[0].Delta.Content
			}
		}

		require.NoError(t, stream.Err())
		require.NotEmpty(t, content)
	})
}

func TestChatCompletionAccumulator(t *testing.T) {
	checkServer(t)
	t.Parallel()

	client := newTestClient()

	t.Run("content accumulation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model: testModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Count from 1 to 5, separated by commas."),
			},
		})

		acc := openai.ChatCompletionAccumulator{}
		contentFinished := false

		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)

			if _, ok := acc.JustFinishedContent(); ok {
				contentFinished = true
			}
		}

		require.NoError(t, stream.Err())
		require.True(t, contentFinished, "JustFinishedContent should have been triggered")
		require.NotEmpty(t, acc.Choices)
		require.NotEmpty(t, acc.Choices[0].Message.Content)
	})

	t.Run("tool call accumulation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model: testModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("What's the weather in Tokyo?"),
			},
			Tools: []openai.ChatCompletionToolUnionParam{
				openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
					Name:        "get_weather",
					Description: openai.String("Get weather for a location"),
					Parameters: openai.FunctionParameters{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{
								"type": "string",
							},
						},
						"required": []string{"location"},
					},
				}),
			},
		})

		acc := openai.ChatCompletionAccumulator{}
		var finishedToolCalls []openai.FinishedChatCompletionToolCall

		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)

			if tool, ok := acc.JustFinishedToolCall(); ok {
				finishedToolCalls = append(finishedToolCalls, tool)
			}
		}

		require.NoError(t, stream.Err())
		require.NotEmpty(t, acc.Choices)

		choice := acc.Choices[0]
		if choice.FinishReason == "tool_calls" {
			require.NotEmpty(t, finishedToolCalls, "JustFinishedToolCall should have been triggered")
			require.NotEmpty(t, finishedToolCalls[0].Name)
			require.NotEmpty(t, finishedToolCalls[0].Arguments)
		}
	})
}

func TestChatCompletionStreamOptions(t *testing.T) {
	checkServer(t)
	t.Parallel()

	client := newTestClient()

	t.Run("include usage", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model: testModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say 'test'."),
			},
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			},
		})

		acc := openai.ChatCompletionAccumulator{}

		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)
		}

		require.NoError(t, stream.Err())
		require.Greater(t, acc.Usage.TotalTokens, int64(0))
	})
}
