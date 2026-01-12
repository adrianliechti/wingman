package anthropic_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/require"
)

const (
	testBaseURL = "http://localhost:8080"
	testTimeout = 60 * time.Second
)

// Test models covering different providers
var testModels = []string{
	"gpt-5.2",           // OpenAI
	"claude-sonnet-4-5", // Anthropic
	"gemini-2.5-pro",    // Google
	"mistral-medium",    // Mistral
}

func newTestClient() anthropic.Client {
	return anthropic.NewClient(
		option.WithBaseURL(testBaseURL),
		option.WithAPIKey("test-key"),
	)
}

func TestMessages(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			tests := []struct {
				name      string
				messages  []anthropic.MessageParam
				system    []anthropic.TextBlockParam
				validator func(t *testing.T, content string)
			}{
				{
					name: "single user message",
					messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Say 'hello' and nothing else.")),
					},
					validator: func(t *testing.T, content string) {
						require.Contains(t, strings.ToLower(content), "hello")
					},
				},
				{
					name: "with system prompt responds in spanish",
					system: []anthropic.TextBlockParam{
						{Text: "You ALWAYS respond in Spanish. Never use English."},
					},
					messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Say hello and introduce yourself briefly.")),
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
					system: []anthropic.TextBlockParam{
						{Text: "You are a helpful assistant."},
					},
					messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("My name is Alice.")),
						anthropic.NewAssistantMessage(anthropic.NewTextBlock("Nice to meet you, Alice!")),
						anthropic.NewUserMessage(anthropic.NewTextBlock("What is my name? Reply with just the name.")),
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

						params := anthropic.MessageNewParams{
							Model:     anthropic.Model(model),
							MaxTokens: 1024,
							Messages:  tt.messages,
						}
						if len(tt.system) > 0 {
							params.System = tt.system
						}

						message, err := client.Messages.New(ctx, params)
						require.NoError(t, err)
						require.NotNil(t, message)
						require.NotEmpty(t, message.Content)
						require.NotEmpty(t, message.StopReason)

						// Extract text content
						var content string
						for _, block := range message.Content {
							if text := block.AsText(); text.Text != "" {
								content += text.Text
							}
						}
						require.NotEmpty(t, content)

						if tt.validator != nil {
							tt.validator(t, content)
						}
					})

					t.Run("streaming", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()

						params := anthropic.MessageNewParams{
							Model:     anthropic.Model(model),
							MaxTokens: 1024,
							Messages:  tt.messages,
						}
						if len(tt.system) > 0 {
							params.System = tt.system
						}

						stream := client.Messages.NewStreaming(ctx, params)

						var content string
						for stream.Next() {
							event := stream.Current()
							switch eventVariant := event.AsAny().(type) {
							case anthropic.ContentBlockDeltaEvent:
								switch deltaVariant := eventVariant.Delta.AsAny().(type) {
								case anthropic.TextDelta:
									content += deltaVariant.Text
								}
							}
						}
						require.NoError(t, stream.Err())
						require.NotEmpty(t, content)

						if tt.validator != nil {
							tt.validator(t, content)
						}
					})
				})
			}
		})
	}
}

func TestMessagesToolCallingMultiTurn(t *testing.T) {
	client := newTestClient()

	weatherTool := anthropic.ToolParam{
		Name:        "get_weather",
		Description: anthropic.String("Get the current weather for a location"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "The city and country, e.g. 'London, UK'",
				},
			},
		},
	}

	tools := []anthropic.ToolUnionParam{
		{OfTool: &weatherTool},
	}

	// Simulated tool execution - returns weather data that should appear in final answer
	executeWeatherTool := func(args any) string {
		// Return a unique weather response that we can verify in the final answer
		return "Sunny, 22°C with light winds from the northwest"
	}

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("non-streaming multi-turn", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				messages := []anthropic.MessageParam{
					anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather in London? Be specific about the conditions.")),
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
					message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
						Model:     anthropic.Model(model),
						MaxTokens: 1024,
						Messages:  messages,
						Tools:     tools,
					})
					require.NoError(t, err)
					require.NotNil(t, message)

					// Check if there are tool uses in the output
					if message.StopReason != anthropic.StopReasonToolUse {
						// Extract final text content
						for _, block := range message.Content {
							if text := block.AsText(); text.Text != "" {
								finalContent += text.Text
							}
						}
						require.Equal(t, anthropic.StopReasonEndTurn, message.StopReason)
						break
					}

					// Add assistant message to conversation
					messages = append(messages, message.ToParam())

					// Process tool uses and add results
					var toolResults []anthropic.ContentBlockParamUnion
					for _, block := range message.Content {
						if toolUse := block.AsToolUse(); toolUse.ID != "" {
							toolResult := executeWeatherTool(toolUse.Input)
							toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUse.ID, toolResult, false))
						}
					}

					// Add tool results as user message
					messages = append(messages, anthropic.NewUserMessage(toolResults...))
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

				messages := []anthropic.MessageParam{
					anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather in Paris, France? Include temperature details.")),
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
					// Accumulate the response
					message := anthropic.Message{}
					stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
						Model:     anthropic.Model(model),
						MaxTokens: 1024,
						Messages:  messages,
						Tools:     tools,
					})

					for stream.Next() {
						event := stream.Current()
						err := message.Accumulate(event)
						require.NoError(t, err)
					}
					require.NoError(t, stream.Err())

					// Check if there are tool uses in the output
					if message.StopReason != anthropic.StopReasonToolUse {
						// Extract final text content
						for _, block := range message.Content {
							if text := block.AsText(); text.Text != "" {
								finalContent += text.Text
							}
						}
						break
					}

					// Add assistant message to conversation
					messages = append(messages, message.ToParam())

					// Process tool uses and add results
					var toolResults []anthropic.ContentBlockParamUnion
					for _, block := range message.Content {
						if toolUse := block.AsToolUse(); toolUse.ID != "" {
							toolResult := executeWeatherTool(toolUse.Input)
							toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUse.ID, toolResult, false))
						}
					}

					// Add tool results as user message
					messages = append(messages, anthropic.NewUserMessage(toolResults...))
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

func TestMessagesAccumulator(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("content accumulation", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				message := anthropic.Message{}
				stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
					Model:     anthropic.Model(model),
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Count from 1 to 5, separated by commas.")),
					},
				})

				for stream.Next() {
					event := stream.Current()
					err := message.Accumulate(event)
					require.NoError(t, err)
				}
				require.NoError(t, stream.Err())
				require.NotEmpty(t, message.Content)

				// Extract text content
				var content string
				for _, block := range message.Content {
					if text := block.AsText(); text.Text != "" {
						content += text.Text
					}
				}
				require.NotEmpty(t, content)
			})

			t.Run("tool use accumulation", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				message := anthropic.Message{}
				stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
					Model:     anthropic.Model(model),
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather in Tokyo?")),
					},
					Tools: []anthropic.ToolUnionParam{
						{OfTool: &anthropic.ToolParam{
							Name:        "get_weather",
							Description: anthropic.String("Get weather for a location"),
							InputSchema: anthropic.ToolInputSchemaParam{
								Properties: map[string]any{
									"location": map[string]any{
										"type": "string",
									},
								},
							},
						}},
					},
				})

				for stream.Next() {
					event := stream.Current()
					err := message.Accumulate(event)
					require.NoError(t, err)
				}
				require.NoError(t, stream.Err())
				require.NotEmpty(t, message.Content)

				// If it's a tool use, verify it has the expected structure
				if message.StopReason == anthropic.StopReasonToolUse {
					var hasToolUse bool
					for _, block := range message.Content {
						if toolUse := block.AsToolUse(); toolUse.ID != "" {
							hasToolUse = true
							require.NotEmpty(t, toolUse.Name)
							require.NotNil(t, toolUse.Input)
						}
					}
					require.True(t, hasToolUse, "expected tool use block when stop reason is tool_use")
				}
			})
		})
	}
}

func TestMessagesUsage(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("non-streaming usage", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
					Model:     anthropic.Model(model),
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Say 'test'.")),
					},
				})
				require.NoError(t, err)
				require.Greater(t, message.Usage.InputTokens, int64(0))
				require.Greater(t, message.Usage.OutputTokens, int64(0))
			})

			t.Run("streaming usage", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				message := anthropic.Message{}
				stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
					Model:     anthropic.Model(model),
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Say 'test'.")),
					},
				})

				for stream.Next() {
					event := stream.Current()
					err := message.Accumulate(event)
					require.NoError(t, err)
				}
				require.NoError(t, stream.Err())

				// OutputTokens comes from message_delta event and should always be present
				// InputTokens ideally comes from message_start, but not all upstream providers
				// provide input token counts during streaming (only in final response)
				require.Greater(t, message.Usage.OutputTokens, int64(0))
			})
		})
	}
}

// BookRecommendation represents a structured book recommendation response
type BookRecommendation struct {
	Title  string   `json:"title"`
	Author string   `json:"author"`
	Year   int      `json:"year"`
	Genres []string `json:"genres"`
	Rating struct {
		Score  float64 `json:"score"`
		Review string  `json:"review"`
	} `json:"rating"`
}

// BookRecommendationSchema is the JSON schema for book recommendations
var BookRecommendationSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title":  map[string]any{"type": "string"},
		"author": map[string]any{"type": "string"},
		"year":   map[string]any{"type": "integer"},
		"genres": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"rating": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"score":  map[string]any{"type": "number"},
				"review": map[string]any{"type": "string"},
			},
			"required":             []string{"score", "review"},
			"additionalProperties": false,
		},
	},
	"required":             []string{"title", "author", "year", "genres", "rating"},
	"additionalProperties": false,
}

func TestMessagesStructuredOutput(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model
		t.Run(model, func(t *testing.T) {
			tests := []struct {
				name   string
				strict bool
			}{
				{"strict mode", true},
				{"non-strict mode", false},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Run("non-streaming", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()

						message, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
							Model:     anthropic.Model(model),
							MaxTokens: 1024,
							Messages: []anthropic.BetaMessageParam{
								anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Recommend a classic science fiction book. Respond with JSON only.")),
							},
							OutputFormat: anthropic.BetaJSONSchemaOutputFormat(BookRecommendationSchema),
							Betas:        []anthropic.AnthropicBeta{"structured-outputs-2025-11-13"},
						})
						require.NoError(t, err)
						require.NotNil(t, message)
						require.NotEmpty(t, message.Content)

						// Extract text content from response
						var content string
						for _, block := range message.Content {
							if block.Type == "text" {
								content = block.Text
								break
							}
						}
						require.NotEmpty(t, content, "expected text content in response")

						var book BookRecommendation
						err = json.Unmarshal([]byte(content), &book)
						require.NoError(t, err, "response should be valid JSON matching schema")
						require.NotEmpty(t, book.Title, "title should be present")
						require.NotEmpty(t, book.Author, "author should be present")
						require.NotZero(t, book.Year, "year should be present")
						require.NotEmpty(t, book.Genres, "genres should be present")
						require.NotZero(t, book.Rating.Score, "rating score should be present")
						require.NotEmpty(t, book.Rating.Review, "rating review should be present")
					})

					t.Run("streaming", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()

						message := anthropic.BetaMessage{}
						stream := client.Beta.Messages.NewStreaming(ctx, anthropic.BetaMessageNewParams{
							Model:     anthropic.Model(model),
							MaxTokens: 1024,
							Messages: []anthropic.BetaMessageParam{
								anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Recommend a classic science fiction book. Respond with JSON only.")),
							},
							OutputFormat: anthropic.BetaJSONSchemaOutputFormat(BookRecommendationSchema),
							Betas:        []anthropic.AnthropicBeta{"structured-outputs-2025-11-13"},
						})

						for stream.Next() {
							event := stream.Current()
							err := message.Accumulate(event)
							require.NoError(t, err)
						}
						require.NoError(t, stream.Err())

						// Extract text content from accumulated message
						var content string
						for _, block := range message.Content {
							if block.Type == "text" {
								content = block.Text
								break
							}
						}
						require.NotEmpty(t, content, "expected text content in response")

						var book BookRecommendation
						err := json.Unmarshal([]byte(content), &book)
						require.NoError(t, err, "response should be valid JSON matching schema")
						require.NotEmpty(t, book.Title, "title should be present")
						require.NotEmpty(t, book.Author, "author should be present")
						require.NotZero(t, book.Year, "year should be present")
						require.NotEmpty(t, book.Genres, "genres should be present")
						require.NotZero(t, book.Rating.Score, "rating score should be present")
						require.NotEmpty(t, book.Rating.Review, "rating review should be present")
					})
				})
			}
		})
	}
}
