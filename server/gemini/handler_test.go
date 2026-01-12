package gemini_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
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

func init() {
	// Set the default base URL for all Gemini API calls
	genai.SetDefaultBaseURLs(genai.BaseURLParameters{
		GeminiURL: testBaseURL,
	})
}

func newTestClient(t *testing.T) *genai.Client {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
	})
	require.NoError(t, err)
	return client
}

func TestGenerateContent(t *testing.T) {
	client := newTestClient(t)

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			tests := []struct {
				name              string
				contents          []*genai.Content
				systemInstruction *genai.Content
				validator         func(t *testing.T, content string)
			}{
				{
					name: "single user message",
					contents: []*genai.Content{
						genai.NewContentFromText("Say 'hello' and nothing else.", genai.RoleUser),
					},
					validator: func(t *testing.T, content string) {
						require.Contains(t, strings.ToLower(content), "hello")
					},
				},
				{
					name: "with system instruction responds in spanish",
					systemInstruction: genai.NewContentFromText(
						"You ALWAYS respond in Spanish. Never use English.",
						"", // system role
					),
					contents: []*genai.Content{
						genai.NewContentFromText("Say hello and introduce yourself briefly.", genai.RoleUser),
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
					systemInstruction: genai.NewContentFromText(
						"You are a helpful assistant.",
						"",
					),
					contents: []*genai.Content{
						genai.NewContentFromText("My name is Alice.", genai.RoleUser),
						genai.NewContentFromText("Nice to meet you, Alice!", genai.RoleModel),
						genai.NewContentFromText("What is my name? Reply with just the name.", genai.RoleUser),
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

						config := &genai.GenerateContentConfig{
							MaxOutputTokens: 1024,
						}
						if tt.systemInstruction != nil {
							config.SystemInstruction = tt.systemInstruction
						}

						response, err := client.Models.GenerateContent(ctx, model, tt.contents, config)
						require.NoError(t, err)
						require.NotNil(t, response)
						require.NotEmpty(t, response.Candidates)

						// Extract text content
						content := response.Text()
						require.NotEmpty(t, content)

						if tt.validator != nil {
							tt.validator(t, content)
						}
					})

					t.Run("streaming", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()

						config := &genai.GenerateContentConfig{
							MaxOutputTokens: 1024,
						}
						if tt.systemInstruction != nil {
							config.SystemInstruction = tt.systemInstruction
						}

						stream := client.Models.GenerateContentStream(ctx, model, tt.contents, config)

						var content string
						for response, err := range stream {
							require.NoError(t, err)
							if response != nil && response.Text() != "" {
								content += response.Text()
							}
						}
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

func TestToolCallingMultiTurn(t *testing.T) {
	client := newTestClient(t)

	weatherTool := &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{
				Name:        "get_weather",
				Description: "Get the current weather for a location",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{
							"type":        "string",
							"description": "The city and country, e.g. 'London, UK'",
						},
					},
				},
			},
		},
	}

	tools := []*genai.Tool{weatherTool}

	// Simulated tool execution - returns weather data that should appear in final answer
	executeWeatherTool := func(args map[string]any) map[string]any {
		return map[string]any{
			"weather": "Sunny, 22°C with light winds from the northwest",
		}
	}

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("non-streaming multi-turn", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				contents := []*genai.Content{
					genai.NewContentFromText("What's the weather in London? Be specific about the conditions.", genai.RoleUser),
				}

				config := &genai.GenerateContentConfig{
					MaxOutputTokens: 1024,
					Tools:           tools,
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
					response, err := client.Models.GenerateContent(ctx, model, contents, config)
					require.NoError(t, err)
					require.NotNil(t, response)

					// Check if there are function calls
					functionCalls := response.FunctionCalls()
					if len(functionCalls) == 0 {
						// No function calls, this is the final response
						finalContent = response.Text()
						break
					}

					// Add assistant message with function call to conversation
					if len(response.Candidates) > 0 && response.Candidates[0].Content != nil {
						contents = append(contents, response.Candidates[0].Content)
					}

					// Process function calls and add results
					var responseParts []*genai.Part
					for _, fc := range functionCalls {
						result := executeWeatherTool(fc.Args)
						// Must set both ID and Name for proper correlation across providers
						responseParts = append(responseParts, &genai.Part{
							FunctionResponse: &genai.FunctionResponse{
								ID:       fc.ID,
								Name:     fc.Name,
								Response: result,
							},
						})
					}

					// Add function response as user message
					contents = append(contents, genai.NewContentFromParts(responseParts, genai.RoleUser))
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

				contents := []*genai.Content{
					genai.NewContentFromText("What's the weather in Paris, France? Include temperature details.", genai.RoleUser),
				}

				config := &genai.GenerateContentConfig{
					MaxOutputTokens: 1024,
					Tools:           tools,
				}

				var finalContent string
				maxIterations := 10 // Safety limit to prevent infinite loops

				for i := 0; i < maxIterations; i++ {
					// Collect streaming response
					var lastResponse *genai.GenerateContentResponse
					var fullText string

					stream := client.Models.GenerateContentStream(ctx, model, contents, config)
					for response, err := range stream {
						require.NoError(t, err)
						if response != nil {
							lastResponse = response
							if response.Text() != "" {
								fullText += response.Text()
							}
						}
					}

					require.NotNil(t, lastResponse)

					// Check if there are function calls
					functionCalls := lastResponse.FunctionCalls()
					if len(functionCalls) == 0 {
						// No function calls, this is the final response
						finalContent = fullText
						break
					}

					// Add assistant message with function call to conversation
					if len(lastResponse.Candidates) > 0 && lastResponse.Candidates[0].Content != nil {
						contents = append(contents, lastResponse.Candidates[0].Content)
					}

					// Process function calls and add results
					var responseParts []*genai.Part
					for _, fc := range functionCalls {
						result := executeWeatherTool(fc.Args)
						// Must set both ID and Name for proper correlation across providers
						responseParts = append(responseParts, &genai.Part{
							FunctionResponse: &genai.FunctionResponse{
								ID:       fc.ID,
								Name:     fc.Name,
								Response: result,
							},
						})
					}

					// Add function response as user message
					contents = append(contents, genai.NewContentFromParts(responseParts, genai.RoleUser))
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

func TestUsage(t *testing.T) {
	client := newTestClient(t)

	for _, model := range testModels {
		model := model // capture range variable
		t.Run(model, func(t *testing.T) {
			t.Run("non-streaming usage", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				contents := []*genai.Content{
					genai.NewContentFromText("Say 'test'.", genai.RoleUser),
				}

				config := &genai.GenerateContentConfig{
					MaxOutputTokens: 1024,
				}

				response, err := client.Models.GenerateContent(ctx, model, contents, config)
				require.NoError(t, err)
				require.NotNil(t, response.UsageMetadata)
				require.Greater(t, response.UsageMetadata.PromptTokenCount, int32(0))
				require.Greater(t, response.UsageMetadata.CandidatesTokenCount, int32(0))
			})

			t.Run("streaming usage", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				contents := []*genai.Content{
					genai.NewContentFromText("Say 'test'.", genai.RoleUser),
				}

				config := &genai.GenerateContentConfig{
					MaxOutputTokens: 1024,
				}

				stream := client.Models.GenerateContentStream(ctx, model, contents, config)

				var lastResponse *genai.GenerateContentResponse
				for response, err := range stream {
					require.NoError(t, err)
					if response != nil {
						lastResponse = response
					}
				}

				// Usage metadata should be available in the final response
				require.NotNil(t, lastResponse)
				if lastResponse.UsageMetadata != nil {
					require.Greater(t, lastResponse.UsageMetadata.CandidatesTokenCount, int32(0))
				}
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

func TestStructuredOutput(t *testing.T) {
	client := newTestClient(t)

	for _, model := range testModels {
		model := model
		t.Run(model, func(t *testing.T) {
			t.Run("non-streaming", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				contents := []*genai.Content{
					genai.NewContentFromText("Recommend a classic science fiction book. Respond with JSON only.", genai.RoleUser),
				}

				config := &genai.GenerateContentConfig{
					MaxOutputTokens:    1024,
					ResponseMIMEType:   "application/json",
					ResponseJsonSchema: BookRecommendationSchema,
				}

				response, err := client.Models.GenerateContent(ctx, model, contents, config)
				require.NoError(t, err)
				require.NotNil(t, response)

				content := response.Text()
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

				contents := []*genai.Content{
					genai.NewContentFromText("Recommend a classic science fiction book. Respond with JSON only.", genai.RoleUser),
				}

				config := &genai.GenerateContentConfig{
					MaxOutputTokens:    1024,
					ResponseMIMEType:   "application/json",
					ResponseJsonSchema: BookRecommendationSchema,
				}

				stream := client.Models.GenerateContentStream(ctx, model, contents, config)

				var content string
				for response, err := range stream {
					require.NoError(t, err)
					if response != nil && response.Text() != "" {
						content += response.Text()
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
}
