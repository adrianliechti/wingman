package chat_test

import (
	"context"
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
	"gpt-5.2",           // OpenAI
	"claude-sonnet-4-5", // Anthropic
	"gemini-2.5-pro",    // Google
	"mistral-medium",    // Mistral (OpenAI-compatible)
}

func newTestClient() openai.Client {
	return openai.NewClient(
		option.WithBaseURL(testBaseURL),
		option.WithAPIKey("test-key"),
	)
}

func TestChatCompletion(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
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
					t.Run("non-streaming", func(t *testing.T) {

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
			t.Run("non-streaming multi-turn", func(t *testing.T) {

				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				messages := []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("What's the weather in London? Be specific about the conditions."),
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
					completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
						Model:    model,
						Messages: messages,
						Tools:    tools,
					})
					require.NoError(t, err)
					require.NotNil(t, completion)
					require.NotEmpty(t, completion.Choices)

					choice := completion.Choices[0]

					// If no tool calls, we're done
					if choice.FinishReason != "tool_calls" {
						finalContent = choice.Message.Content
						require.Equal(t, "stop", string(choice.FinishReason))
						break
					}

					// Process all tool calls
					require.NotEmpty(t, choice.Message.ToolCalls)
					messages = append(messages, choice.Message.ToParam())

					for _, toolCall := range choice.Message.ToolCalls {
						require.Equal(t, "get_weather", toolCall.Function.Name)
						require.NotEmpty(t, toolCall.ID)

						toolResult := executeWeatherTool(toolCall.Function.Arguments)
						messages = append(messages, openai.ToolMessage(toolResult, toolCall.ID))
					}
				}

				// Verify final response includes data from tool result
				require.NotEmpty(t, finalContent, "expected final response after tool execution")

				lower := strings.ToLower(finalContent)
				hasWeatherInfo := strings.Contains(lower, "sunny") ||
					strings.Contains(lower, "22") ||
					strings.Contains(lower, "wind") ||
					strings.Contains(lower, "northwest")
				require.True(t, hasWeatherInfo, "expected final response to include weather data from tool, got: %s", finalContent)
			})

			t.Run("streaming multi-turn", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				messages := []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("What's the weather in Paris, France? Include temperature details."),
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
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

					// If no tool calls, we're done
					if choice.FinishReason != "tool_calls" {
						finalContent = choice.Message.Content
						require.Equal(t, "stop", choice.FinishReason)
						break
					}

					// Process all tool calls
					require.NotEmpty(t, choice.Message.ToolCalls)
					messages = append(messages, choice.Message.ToParam())

					for _, toolCall := range choice.Message.ToolCalls {
						require.Equal(t, "get_weather", toolCall.Function.Name)
						require.NotEmpty(t, toolCall.ID)

						toolResult := executeWeatherTool(toolCall.Function.Arguments)
						messages = append(messages, openai.ToolMessage(toolResult, toolCall.ID))
					}
				}

				// Verify final response includes data from tool result
				require.NotEmpty(t, finalContent, "expected final response after tool execution")

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
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("content accumulation", func(t *testing.T) {

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
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("include usage", func(t *testing.T) {

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
