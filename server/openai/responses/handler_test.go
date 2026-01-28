package responses_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
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

func requireUsage(t *testing.T, usage responses.ResponseUsage) {
	require.Greater(t, usage.TotalTokens, int64(0))
	require.Equal(t, usage.TotalTokens, usage.InputTokens+usage.OutputTokens)
}

func TestResponses(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			tests := []struct {
				name      string
				input     responses.ResponseNewParamsInputUnion
				validator func(t *testing.T, content string)
			}{
				{
					name: "single user message string",
					input: responses.ResponseNewParamsInputUnion{
						OfString: openai.String("Say 'hello' and nothing else."),
					},
					validator: func(t *testing.T, content string) {
						require.Contains(t, strings.ToLower(content), "hello")
					},
				},
				{
					name: "single user message with input items",
					input: responses.ResponseNewParamsInputUnion{
						OfInputItemList: []responses.ResponseInputItemUnionParam{
							{
								OfMessage: &responses.EasyInputMessageParam{
									Role: responses.EasyInputMessageRoleUser,
									Content: responses.EasyInputMessageContentUnionParam{
										OfString: openai.String("Say 'hello' and nothing else."),
									},
								},
							},
						},
					},
					validator: func(t *testing.T, content string) {
						require.Contains(t, strings.ToLower(content), "hello")
					},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Run("non-streaming", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()

						resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
							Model: model,
							Input: tt.input,
						})
						require.NoError(t, err)
						require.NotNil(t, resp)
						require.Equal(t, responses.ResponseStatusCompleted, resp.Status)

						outputText := resp.OutputText()
						require.NotEmpty(t, outputText)

						if tt.validator != nil {
							tt.validator(t, outputText)
						}
					})

					t.Run("streaming", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()

						stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
							Model: model,
							Input: tt.input,
						})

						var content string

						for stream.Next() {
							data := stream.Current()
							content += data.Delta
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

func TestResponsesStreamingCompletedIncludesText(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
				Model: model,
				Input: responses.ResponseNewParamsInputUnion{
					OfString: openai.String("Say hello in one short sentence."),
				},
			})

			var streamed string
			var completedText string

			for stream.Next() {
				data := stream.Current()
				streamed += data.Delta

				if data.Response.Status == responses.ResponseStatusCompleted {
					completedText = data.Response.OutputText()
				}
			}

			require.NoError(t, stream.Err())
			require.NotEmpty(t, streamed)
			require.NotEmpty(t, completedText)

			trimmedStreamed := strings.TrimSpace(streamed)
			trimmedCompleted := strings.TrimSpace(completedText)
			require.True(
				t,
				strings.Contains(trimmedCompleted, trimmedStreamed) || strings.Contains(trimmedStreamed, trimmedCompleted),
				"completed output should include streamed text (streamed=%q completed=%q)",
				trimmedStreamed,
				trimmedCompleted,
			)
		})
	}
}

func TestResponsesWithInstructions(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("responds in spanish with instructions", func(t *testing.T) {
				t.Run("non-streaming", func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					defer cancel()

					resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
						Model:        model,
						Instructions: openai.String("You ALWAYS respond in Spanish. Never use English."),
						Input: responses.ResponseNewParamsInputUnion{
							OfString: openai.String("Say hello and introduce yourself briefly."),
						},
					})
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Equal(t, responses.ResponseStatusCompleted, resp.Status)

					outputText := resp.OutputText()
					lower := strings.ToLower(outputText)

					// Check for common Spanish words
					hasSpanish := strings.Contains(lower, "hola") ||
						strings.Contains(lower, "soy") ||
						strings.Contains(lower, "buenos") ||
						strings.Contains(lower, "como") ||
						strings.Contains(lower, "estoy") ||
						strings.Contains(lower, "puedo") ||
						strings.Contains(lower, "ayudar")
					require.True(t, hasSpanish, "expected Spanish response, got: %s", outputText)
				})

				t.Run("streaming", func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					defer cancel()

					stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
						Model:        model,
						Instructions: openai.String("You ALWAYS respond in Spanish. Never use English."),
						Input: responses.ResponseNewParamsInputUnion{
							OfString: openai.String("Say hello and introduce yourself briefly."),
						},
					})

					var content string

					for stream.Next() {
						data := stream.Current()
						content += data.Delta
					}

					require.NoError(t, stream.Err())

					lower := strings.ToLower(content)
					hasSpanish := strings.Contains(lower, "hola") ||
						strings.Contains(lower, "soy") ||
						strings.Contains(lower, "buenos") ||
						strings.Contains(lower, "como") ||
						strings.Contains(lower, "estoy") ||
						strings.Contains(lower, "puedo") ||
						strings.Contains(lower, "ayudar")
					require.True(t, hasSpanish, "expected Spanish response, got: %s", content)
				})
			})
		})
	}
}

func TestResponsesMultiTurnConversation(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("remembers context", func(t *testing.T) {
				t.Run("non-streaming", func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					defer cancel()

					resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
						Model:        model,
						Instructions: openai.String("You are a helpful assistant."),
						Input: responses.ResponseNewParamsInputUnion{
							OfInputItemList: []responses.ResponseInputItemUnionParam{
								{
									OfMessage: &responses.EasyInputMessageParam{
										Role: responses.EasyInputMessageRoleUser,
										Content: responses.EasyInputMessageContentUnionParam{
											OfString: openai.String("My name is Alice."),
										},
									},
								},
								{
									OfMessage: &responses.EasyInputMessageParam{
										Role: responses.EasyInputMessageRoleAssistant,
										Content: responses.EasyInputMessageContentUnionParam{
											OfString: openai.String("Nice to meet you, Alice!"),
										},
									},
								},
								{
									OfMessage: &responses.EasyInputMessageParam{
										Role: responses.EasyInputMessageRoleUser,
										Content: responses.EasyInputMessageContentUnionParam{
											OfString: openai.String("What is my name? Reply with just the name."),
										},
									},
								},
							},
						},
					})
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Contains(t, resp.OutputText(), "Alice")
				})

				t.Run("streaming", func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					defer cancel()

					stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
						Model:        model,
						Instructions: openai.String("You are a helpful assistant."),
						Input: responses.ResponseNewParamsInputUnion{
							OfInputItemList: []responses.ResponseInputItemUnionParam{
								{
									OfMessage: &responses.EasyInputMessageParam{
										Role: responses.EasyInputMessageRoleUser,
										Content: responses.EasyInputMessageContentUnionParam{
											OfString: openai.String("My name is Alice."),
										},
									},
								},
								{
									OfMessage: &responses.EasyInputMessageParam{
										Role: responses.EasyInputMessageRoleAssistant,
										Content: responses.EasyInputMessageContentUnionParam{
											OfString: openai.String("Nice to meet you, Alice!"),
										},
									},
								},
								{
									OfMessage: &responses.EasyInputMessageParam{
										Role: responses.EasyInputMessageRoleUser,
										Content: responses.EasyInputMessageContentUnionParam{
											OfString: openai.String("What is my name? Reply with just the name."),
										},
									},
								},
							},
						},
					})

					var content string

					for stream.Next() {
						data := stream.Current()
						content += data.Delta
					}

					require.NoError(t, stream.Err())
					require.Contains(t, content, "Alice")
				})
			})
		})
	}
}

func TestResponsesToolCallingMultiTurn(t *testing.T) {
	client := newTestClient()

	weatherTool := responses.FunctionToolParam{
		Name:        "get_weather",
		Description: openai.String("Get the current weather for a location"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "The city and country, e.g. 'London, UK'",
				},
			},
			"required": []string{"location"},
		},
	}

	tools := []responses.ToolUnionParam{
		{OfFunction: &weatherTool},
	}

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

				inputItems := []responses.ResponseInputItemUnionParam{
					{
						OfMessage: &responses.EasyInputMessageParam{
							Role: responses.EasyInputMessageRoleUser,
							Content: responses.EasyInputMessageContentUnionParam{
								OfString: openai.String("What's the weather in London? Be specific about the conditions."),
							},
						},
					},
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
					resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
						Model: model,
						Input: responses.ResponseNewParamsInputUnion{
							OfInputItemList: inputItems,
						},
						Tools: tools,
					})
					require.NoError(t, err)
					require.NotNil(t, resp)

					// Check if there are function calls in the output
					hasFunctionCalls := false
					for _, item := range resp.Output {
						if item.Type == "function_call" {
							hasFunctionCalls = true
							fc := item.AsFunctionCall()

							// Add the function call to input items
							inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
								OfFunctionCall: &responses.ResponseFunctionToolCallParam{
									CallID:    fc.CallID,
									Name:      fc.Name,
									Arguments: fc.Arguments,
								},
							})

							// Execute the tool and add the result
							toolResult := executeWeatherTool(fc.Arguments)
							inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
								OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
									CallID: fc.CallID,
									Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
										OfString: openai.String(toolResult),
									},
								},
							})
						}
					}

					// If no function calls, we're done
					if !hasFunctionCalls {
						finalContent = resp.OutputText()
						require.Equal(t, responses.ResponseStatusCompleted, resp.Status)
						break
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

				inputItems := []responses.ResponseInputItemUnionParam{
					{
						OfMessage: &responses.EasyInputMessageParam{
							Role: responses.EasyInputMessageRoleUser,
							Content: responses.EasyInputMessageContentUnionParam{
								OfString: openai.String("What's the weather in Paris, France? Include temperature details."),
							},
						},
					},
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
					stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
						Model: model,
						Input: responses.ResponseNewParamsInputUnion{
							OfInputItemList: inputItems,
						},
						Tools: tools,
					})

					// Accumulate the response
					var textContent string
					var functionCalls []struct {
						CallID    string
						Name      string
						Arguments string
					}

					for stream.Next() {
						data := stream.Current()
						textContent += data.Delta

						// Check for function calls in the completed response
						if data.Response.Status == "completed" {
							for _, item := range data.Response.Output {
								if item.Type == "function_call" {
									fc := item.AsFunctionCall()
									functionCalls = append(functionCalls, struct {
										CallID    string
										Name      string
										Arguments string
									}{
										CallID:    fc.CallID,
										Name:      fc.Name,
										Arguments: fc.Arguments,
									})
								}
							}
						}
					}
					require.NoError(t, stream.Err())

					// If no function calls, we're done
					if len(functionCalls) == 0 {
						finalContent = textContent
						break
					}

					// Process function calls
					for _, fc := range functionCalls {
						inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
							OfFunctionCall: &responses.ResponseFunctionToolCallParam{
								CallID:    fc.CallID,
								Name:      fc.Name,
								Arguments: fc.Arguments,
							},
						})

						toolResult := executeWeatherTool(fc.Arguments)
						inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
							OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
								CallID: fc.CallID,
								Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
									OfString: openai.String(toolResult),
								},
							},
						})
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

func TestResponsesStreamOptions(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("include usage", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
					Model: model,
					Input: responses.ResponseNewParamsInputUnion{
						OfString: openai.String("Say 'test'."),
					},
					StreamOptions: responses.ResponseNewParamsStreamOptions{
						IncludeObfuscation: openai.Bool(false),
					},
				})

				var lastResponse responses.Response

				for stream.Next() {
					data := stream.Current()
					if data.Response.ID != "" {
						lastResponse = data.Response
					}
				}

				require.NoError(t, stream.Err())
				require.NotEmpty(t, lastResponse.ID)
				requireUsage(t, lastResponse.Usage)
			})
		})
	}

}

func TestResponsesUsage(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
				Model: model,
				Input: responses.ResponseNewParamsInputUnion{
					OfString: openai.String("Say 'test'."),
				},
			})
			require.NoError(t, err)
			require.NotNil(t, resp)
			requireUsage(t, resp.Usage)
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

func TestResponsesStructuredOutput(t *testing.T) {
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

						resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
							Model: model,
							Input: responses.ResponseNewParamsInputUnion{
								OfString: openai.String("Recommend a classic science fiction book. Respond with JSON only."),
							},
							Text: responses.ResponseTextConfigParam{
								Format: responses.ResponseFormatTextConfigUnionParam{
									OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
										Name:   "book_recommendation",
										Schema: BookRecommendationSchema,
										Strict: openai.Bool(tt.strict),
									},
								},
							},
						})
						require.NoError(t, err)
						require.NotNil(t, resp)
						require.Equal(t, responses.ResponseStatusCompleted, resp.Status)

						content := resp.OutputText()

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

						stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
							Model: model,
							Input: responses.ResponseNewParamsInputUnion{
								OfString: openai.String("Recommend a classic science fiction book. Respond with JSON only."),
							},
							Text: responses.ResponseTextConfigParam{
								Format: responses.ResponseFormatTextConfigUnionParam{
									OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
										Name:   "book_recommendation",
										Schema: BookRecommendationSchema,
										Strict: openai.Bool(tt.strict),
									},
								},
							},
						})

						var content string
						for stream.Next() {
							data := stream.Current()
							content += data.Delta
						}
						require.NoError(t, stream.Err())

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

// SimpleAnswer represents a simple JSON response with an answer field
type SimpleAnswer struct {
	Answer int `json:"answer"`
}

func TestResponsesJSONObjectFormat(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels {
		model := model
		t.Run(model, func(t *testing.T) {
			t.Run("non-streaming", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
					Model:        model,
					Instructions: openai.String("You are a helpful assistant that responds only in valid JSON format. Never include markdown formatting, code blocks, or any text outside the JSON object."),
					Input: responses.ResponseNewParamsInputUnion{
						OfString: openai.String(`Respond with exactly this JSON object: {"answer": 42}`),
					},
					Text: responses.ResponseTextConfigParam{
						Format: responses.ResponseFormatTextConfigUnionParam{
							OfJSONObject: &responses.ResponseFormatJSONObjectParam{},
						},
					},
				})
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, responses.ResponseStatusCompleted, resp.Status)

				content := resp.OutputText()

				var result SimpleAnswer
				err = json.Unmarshal([]byte(content), &result)
				require.NoError(t, err, "response should be valid JSON, got: %s", content)
				require.Equal(t, 42, result.Answer, "answer field should be 42")
			})

			t.Run("streaming", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
					Model:        model,
					Instructions: openai.String("You are a helpful assistant that responds only in valid JSON format. Never include markdown formatting, code blocks, or any text outside the JSON object."),
					Input: responses.ResponseNewParamsInputUnion{
						OfString: openai.String(`Respond with exactly this JSON object: {"answer": 42}`),
					},
					Text: responses.ResponseTextConfigParam{
						Format: responses.ResponseFormatTextConfigUnionParam{
							OfJSONObject: &responses.ResponseFormatJSONObjectParam{},
						},
					},
				})

				var content string
				for stream.Next() {
					data := stream.Current()
					content += data.Delta
				}
				require.NoError(t, stream.Err())

				var result SimpleAnswer
				err := json.Unmarshal([]byte(content), &result)
				require.NoError(t, err, "response should be valid JSON, got: %s", content)
				require.Equal(t, 42, result.Answer, "answer field should be 42")
			})
		})
	}
}

// Models that support reasoning/thinking
var reasoningModels = []string{
	"gpt-5.2", // OpenAI reasoning model
}

func TestResponsesWithReasoning(t *testing.T) {
	client := newTestClient()

	for _, model := range reasoningModels {
		model := model
		t.Run(model, func(t *testing.T) {
			t.Run("non-streaming with reasoning", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
					Model: model,
					Input: responses.ResponseNewParamsInputUnion{
						OfString: openai.String("What is 15 * 17? Think step by step."),
					},
					Reasoning: responses.ReasoningParam{
						Effort:  responses.ReasoningEffortLow,
						Summary: responses.ReasoningSummaryAuto,
					},
				})
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, responses.ResponseStatusCompleted, resp.Status)

				// Should have output text with the answer
				outputText := resp.OutputText()
				require.NotEmpty(t, outputText)
				require.Contains(t, outputText, "255")
			})

			t.Run("streaming with reasoning", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
					Model: model,
					Input: responses.ResponseNewParamsInputUnion{
						OfString: openai.String("What is 15 * 17? Think step by step."),
					},
					Reasoning: responses.ReasoningParam{
						Effort:  responses.ReasoningEffortLow,
						Summary: responses.ReasoningSummaryAuto,
					},
				})

				var textContent string
				var hasReasoningItem bool
				var hasReasoningSummary bool

				for stream.Next() {
					data := stream.Current()
					textContent += data.Delta

					// Check for reasoning items in output
					if data.Response.Status == responses.ResponseStatusCompleted {
						for _, item := range data.Response.Output {
							if item.Type == "reasoning" {
								hasReasoningItem = true
								if len(item.Summary) > 0 {
									hasReasoningSummary = true
								}
							}
						}
					}
				}

				require.NoError(t, stream.Err())
				require.NotEmpty(t, textContent)
				require.Contains(t, textContent, "255")
				// Note: hasReasoningItem may be false if the model doesn't return reasoning
				// This depends on the upstream provider's response
				_ = hasReasoningItem
				_ = hasReasoningSummary
			})
		})
	}
}

func TestResponsesReasoningStreamEvents(t *testing.T) {
	client := newTestClient()

	for _, model := range reasoningModels {
		model := model
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
				Model: model,
				Input: responses.ResponseNewParamsInputUnion{
					OfString: openai.String("What is 23 + 19?"),
				},
				Reasoning: responses.ReasoningParam{
					Effort:  responses.ReasoningEffortLow,
					Summary: responses.ReasoningSummaryAuto,
				},
			})

			eventTypes := make(map[string]int)
			var finalResponse *responses.Response

			for stream.Next() {
				data := stream.Current()
				eventTypes[data.Type]++

				if data.Response.Status == responses.ResponseStatusCompleted {
					finalResponse = &data.Response
				}
			}

			require.NoError(t, stream.Err())
			require.NotNil(t, finalResponse)

			// Should have basic response events
			require.Greater(t, eventTypes["response.created"], 0, "should have response.created event")
			require.Greater(t, eventTypes["response.completed"], 0, "should have response.completed event")

			// Should have text output
			outputText := finalResponse.OutputText()
			require.NotEmpty(t, outputText)
			require.Contains(t, outputText, "42")
		})
	}
}

func TestResponsesReasoningEncryptedContent(t *testing.T) {
	client := newTestClient()

	for _, model := range reasoningModels {
		model := model
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			// First request with reasoning
			resp1, err := client.Responses.New(ctx, responses.ResponseNewParams{
				Model: model,
				Input: responses.ResponseNewParamsInputUnion{
					OfString: openai.String("Remember the number 7. Just say 'OK'."),
				},
				Reasoning: responses.ReasoningParam{
					Effort:  responses.ReasoningEffortLow,
					Summary: responses.ReasoningSummaryAuto,
				},
			})
			require.NoError(t, err)
			require.NotNil(t, resp1)

			// Build input for second request including previous output
			var inputItems []responses.ResponseInputItemUnionParam

			// Add original user message
			inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfString: openai.String("Remember the number 7. Just say 'OK'."),
					},
				},
			})

			// Add previous response output
			for _, item := range resp1.Output {
				switch item.Type {
				case "message":
					outputMsg := &responses.ResponseOutputMessageParam{
						Content: []responses.ResponseOutputMessageContentUnionParam{},
					}
					for _, content := range item.Content {
						if content.Type == "output_text" {
							outputMsg.Content = append(outputMsg.Content, responses.ResponseOutputMessageContentUnionParam{
								OfOutputText: &responses.ResponseOutputTextParam{
									Text: content.Text,
								},
							})
						}
					}
					if len(outputMsg.Content) > 0 {
						inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
							OfOutputMessage: outputMsg,
						})
					}

				case "reasoning":
					// Include reasoning with encrypted_content for continuity
					reasoningItem := &responses.ResponseReasoningItemParam{
						ID:      item.ID,
						Summary: []responses.ResponseReasoningItemSummaryParam{},
					}
					for _, s := range item.Summary {
						reasoningItem.Summary = append(reasoningItem.Summary, responses.ResponseReasoningItemSummaryParam{
							Text: s.Text,
						})
					}
					if item.EncryptedContent != "" {
						reasoningItem.EncryptedContent = openai.String(item.EncryptedContent)
					}
					inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
						OfReasoning: reasoningItem,
					})
				}
			}

			// Add follow-up question
			inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfString: openai.String("What number did I ask you to remember?"),
					},
				},
			})

			// Second request should remember the context
			resp2, err := client.Responses.New(ctx, responses.ResponseNewParams{
				Model: model,
				Input: responses.ResponseNewParamsInputUnion{
					OfInputItemList: inputItems,
				},
				Reasoning: responses.ReasoningParam{
					Effort:  responses.ReasoningEffortLow,
					Summary: responses.ReasoningSummaryAuto,
				},
			})
			require.NoError(t, err)
			require.NotNil(t, resp2)

			outputText := resp2.OutputText()
			require.Contains(t, outputText, "7", "should remember the number 7")
		})
	}
}
