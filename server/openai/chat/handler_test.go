package chat_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/stretchr/testify/require"
)

const (
	testBaseURL = "http://localhost:8080/v1/"
	testTimeout = 60 * time.Second
)

// Test models covering different providers
var testModels = []string{
	"gpt-5.2",              // OpenAI
	"claude-sonnet-4-5",    // Anthropic
	"gemini-3-pro-preview", // Google
}

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

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name      string
				messages  []openai.ChatCompletionMessageParamUnion
				validator func(t *testing.T, content string)
			}{
				{
					name: "single user message",
					messages: []openai.ChatCompletionMessageParamUnion{
						openai.UserMessage("Say 'hello' and nothing else."),
					},
					validator: func(t *testing.T, content string) {
						require.Contains(t, strings.ToLower(content), "hello")
					},
				},
				{
					name: "with system prompt responds in spanish",
					messages: []openai.ChatCompletionMessageParamUnion{
						openai.SystemMessage("You ALWAYS respond in Spanish. Never use English."),
						openai.UserMessage("Say hello and introduce yourself briefly."),
					},
					validator: func(t *testing.T, content string) {
						lower := strings.ToLower(content)
						// Check for common Spanish words
						hasSpanish := strings.Contains(lower, "hola") ||
							strings.Contains(lower, "soy") ||
							strings.Contains(lower, "buenos") ||
							strings.Contains(lower, "como") ||
							strings.Contains(lower, "estoy") ||
							strings.Contains(lower, "puedo") ||
							strings.Contains(lower, "ayudar")
						require.True(t, hasSpanish, "expected Spanish response, got: %s", content)
					},
				},
				{
					name: "multi-turn conversation remembers context",
					messages: []openai.ChatCompletionMessageParamUnion{
						openai.SystemMessage("You are a helpful assistant."),
						openai.UserMessage("My name is Alice."),
						openai.AssistantMessage("Nice to meet you, Alice!"),
						openai.UserMessage("What is my name? Reply with just the name."),
					},
					validator: func(t *testing.T, content string) {
						require.Contains(t, content, "Alice")
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
							Model:    model,
							Messages: tt.messages,
						})
						require.NoError(t, err)
						require.NotNil(t, completion)
						require.NotEmpty(t, completion.Choices)
						require.NotEmpty(t, completion.Choices[0].Message.Content)
						require.NotEmpty(t, completion.Choices[0].FinishReason)

						if tt.validator != nil {
							tt.validator(t, completion.Choices[0].Message.Content)
						}
					})

					t.Run("streaming", func(t *testing.T) {
						t.Parallel()

						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()

						stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
							Model:    model,
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

						if tt.validator != nil {
							tt.validator(t, content)
						}
					})
				})
			}
		})
	}
}

func TestChatCompletionToolCallingMultiTurn(t *testing.T) {
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

	tools := []openai.ChatCompletionToolUnionParam{weatherTool}

	// Simulated tool execution - returns weather data that should appear in final response
	executeWeatherTool := func(args string) string {
		// Return a unique weather response that we can verify in the final answer
		return "Sunny, 22°C with light winds from the northwest"
	}

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Parallel()

			t.Run("non-streaming multi-turn", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				// Step 1: Initial request - should trigger tool call
				messages := []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("What's the weather in London? Be specific about the conditions."),
				}

				completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
					Model:    model,
					Messages: messages,
					Tools:    tools,
				})
				require.NoError(t, err)
				require.NotNil(t, completion)
				require.NotEmpty(t, completion.Choices)

				choice := completion.Choices[0]
				require.Equal(t, "tool_calls", string(choice.FinishReason), "expected model to call tool")
				require.NotEmpty(t, choice.Message.ToolCalls)

				toolCall := choice.Message.ToolCalls[0]
				require.Equal(t, "get_weather", toolCall.Function.Name)
				require.NotEmpty(t, toolCall.ID)
				require.Contains(t, strings.ToLower(toolCall.Function.Arguments), "london")

				// Step 2: Execute tool and send result back
				toolResult := executeWeatherTool(toolCall.Function.Arguments)

				messages = append(messages,
					completion.Choices[0].Message.ToParam(),
					openai.ToolMessage(toolResult, toolCall.ID),
				)

				// Step 3: Get final response that incorporates tool result
				finalCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
					Model:    model,
					Messages: messages,
					Tools:    tools,
				})
				require.NoError(t, err)
				require.NotNil(t, finalCompletion)
				require.NotEmpty(t, finalCompletion.Choices)
				require.Equal(t, "stop", string(finalCompletion.Choices[0].FinishReason))

				// Verify final response includes data from tool result
				finalContent := finalCompletion.Choices[0].Message.Content
				require.NotEmpty(t, finalContent)

				lower := strings.ToLower(finalContent)
				hasWeatherInfo := strings.Contains(lower, "sunny") ||
					strings.Contains(lower, "22") ||
					strings.Contains(lower, "wind") ||
					strings.Contains(lower, "northwest")
				require.True(t, hasWeatherInfo, "expected final response to include weather data from tool, got: %s", finalContent)
			})

			t.Run("streaming multi-turn", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				// Step 1: Initial streaming request - should trigger tool call
				messages := []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("What's the weather in Paris? Include temperature details."),
				}

				stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
					Model:    model,
					Messages: messages,
					Tools:    tools,
				})

				acc := openai.ChatCompletionAccumulator{}
				for stream.Next() {
					acc.AddChunk(stream.Current())
				}
				require.NoError(t, stream.Err())
				require.NotEmpty(t, acc.Choices)

				choice := acc.Choices[0]
				require.Equal(t, "tool_calls", choice.FinishReason, "expected model to call tool")
				require.NotEmpty(t, choice.Message.ToolCalls)

				toolCall := choice.Message.ToolCalls[0]
				require.Equal(t, "get_weather", toolCall.Function.Name)
				require.NotEmpty(t, toolCall.ID)
				require.Contains(t, strings.ToLower(toolCall.Function.Arguments), "paris")

				// Step 2: Execute tool and send result back
				toolResult := executeWeatherTool(toolCall.Function.Arguments)

				// Build assistant message from accumulated response
				messages = append(messages,
					acc.Choices[0].Message.ToParam(),
					openai.ToolMessage(toolResult, toolCall.ID),
				)

				// Step 3: Stream final response that incorporates tool result
				finalStream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
					Model:    model,
					Messages: messages,
					Tools:    tools,
				})

				var finalContent string
				var finalFinishReason string
				for finalStream.Next() {
					chunk := finalStream.Current()
					if len(chunk.Choices) > 0 {
						finalContent += chunk.Choices[0].Delta.Content
						if chunk.Choices[0].FinishReason != "" {
							finalFinishReason = string(chunk.Choices[0].FinishReason)
						}
					}
				}
				require.NoError(t, finalStream.Err())
				require.Equal(t, "stop", finalFinishReason)
				require.NotEmpty(t, finalContent)

				// Verify final response includes data from tool result
				lower := strings.ToLower(finalContent)
				hasWeatherInfo := strings.Contains(lower, "sunny") ||
					strings.Contains(lower, "22") ||
					strings.Contains(lower, "wind") ||
					strings.Contains(lower, "northwest")
				require.True(t, hasWeatherInfo, "expected final response to include weather data from tool, got: %s", finalContent)
			})
		})
	}
}

func TestChatCompletionAccumulator(t *testing.T) {
	checkServer(t)
	t.Parallel()

	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Parallel()

			t.Run("content accumulation", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
					Model: model,
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
					Model: model,
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
		})
	}
}

func TestChatCompletionStreamOptions(t *testing.T) {
	checkServer(t)
	t.Parallel()

	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Parallel()

			t.Run("include usage", func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
					Model: model,
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
		})
	}
}
