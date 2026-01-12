package embeddings_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
)

const (
	testBaseURL = "http://localhost:8080/v1/"
	testTimeout = 60 * time.Second
)

// Model-specific dimension configurations for testing
var testModelDimensions = map[string][]int{
	"text-embedding-3-small": {256, 512, 1024},
	"gemini-embedding-001":   {256, 512, 768},
	"jina-embeddings-v3":     {256, 512, 1024},
}

func newTestClient() openai.Client {
	return openai.NewClient(
		option.WithBaseURL(testBaseURL),
		option.WithAPIKey("test-key"),
	)
}

func testModels() []string {
	models := make([]string, 0, len(testModelDimensions))
	for model := range testModelDimensions {
		models = append(models, model)
	}
	return models
}

func TestEmbeddingSingle(t *testing.T) {
	client := newTestClient()

	for _, model := range testModels() {
		model := model
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			embedding, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
				Model: model,
				Input: openai.EmbeddingNewParamsInputUnion{
					OfString: openai.String("Hello, World!"),
				},
			})

			require.NoError(t, err)
			require.NotNil(t, embedding)
			require.Equal(t, "list", string(embedding.Object))
			require.Len(t, embedding.Data, 1)
			require.Equal(t, "embedding", string(embedding.Data[0].Object))
			require.Equal(t, int64(0), embedding.Data[0].Index)
			require.NotEmpty(t, embedding.Data[0].Embedding)
		})
	}
}

func TestEmbeddingBatch(t *testing.T) {
	client := newTestClient()

	inputs := []string{
		"Hello, World!",
		"The quick brown fox jumps over the lazy dog.",
		"Machine learning is fascinating.",
	}

	for _, model := range testModels() {
		model := model
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			embedding, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
				Model: model,
				Input: openai.EmbeddingNewParamsInputUnion{
					OfArrayOfStrings: inputs,
				},
			})

			require.NoError(t, err)
			require.NotNil(t, embedding)
			require.Equal(t, "list", string(embedding.Object))
			require.Len(t, embedding.Data, len(inputs))

			for i, data := range embedding.Data {
				require.Equal(t, "embedding", string(data.Object))
				require.Equal(t, int64(i), data.Index)
				require.NotEmpty(t, data.Embedding, "embedding at index %d should not be empty", i)
			}
		})
	}
}

func TestEmbeddingDimensions(t *testing.T) {
	client := newTestClient()

	for model, dimensions := range testModelDimensions {
		model := model
		dimensions := dimensions
		t.Run(model, func(t *testing.T) {
			for _, dim := range dimensions {
				dim := dim
				t.Run(fmt.Sprintf("dim_%d", dim), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					defer cancel()

					embedding, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
						Model: model,
						Input: openai.EmbeddingNewParamsInputUnion{
							OfString: openai.String("Hello, World!"),
						},
						Dimensions: openai.Int(int64(dim)),
					})

					require.NoError(t, err)
					require.NotNil(t, embedding)
					require.Len(t, embedding.Data, 1)
					require.Len(t, embedding.Data[0].Embedding, dim, "expected embedding dimension %d, got %d", dim, len(embedding.Data[0].Embedding))
				})
			}
		})
	}
}

func TestEmbeddingEncodingFormat(t *testing.T) {
	client := newTestClient()
	model := "text-embedding-3-small"
	dim := 256

	t.Run("float", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		embedding, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
			Model: model,
			Input: openai.EmbeddingNewParamsInputUnion{
				OfString: openai.String("Hello, World!"),
			},
			Dimensions:     openai.Int(int64(dim)),
			EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
		})

		require.NoError(t, err)
		require.NotNil(t, embedding)
		require.Len(t, embedding.Data, 1)
		require.Len(t, embedding.Data[0].Embedding, dim)

		// Verify values are valid floats (not NaN or Inf)
		for i, v := range embedding.Data[0].Embedding {
			require.False(t, math.IsNaN(float64(v)), "embedding[%d] is NaN", i)
			require.False(t, math.IsInf(float64(v), 0), "embedding[%d] is Inf", i)
		}
	})

	t.Run("base64", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		embedding, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
			Model: model,
			Input: openai.EmbeddingNewParamsInputUnion{
				OfString: openai.String("Hello, World!"),
			},
			Dimensions:     openai.Int(int64(dim)),
			EncodingFormat: openai.EmbeddingNewParamsEncodingFormatBase64,
		})

		require.NoError(t, err)
		require.NotNil(t, embedding)
		require.Len(t, embedding.Data, 1)

		// The SDK doesn't automatically decode base64 embeddings.
		// We need to extract the base64 string from the raw JSON and decode it manually.
		rawJSON := embedding.Data[0].RawJSON()
		require.NotEmpty(t, rawJSON)

		// Parse raw JSON to get base64 string
		var rawData struct {
			Embedding string `json:"embedding"`
		}
		err = json.Unmarshal([]byte(rawJSON), &rawData)
		require.NoError(t, err)
		require.NotEmpty(t, rawData.Embedding, "base64 embedding should not be empty")

		// Decode base64 and verify dimensions
		decoded, err := decodeBase64Embedding(rawData.Embedding)
		require.NoError(t, err)
		require.Len(t, decoded, dim)
	})
}

func TestEmbeddingBase64Decode(t *testing.T) {
	// Test that base64 encoding produces valid little-endian float32 data
	// This tests the encoding directly without relying on SDK decoding behavior

	client := newTestClient()
	model := "text-embedding-3-small"
	dim := 256

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Get float encoding first for comparison
	floatEmbedding, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: model,
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String("Hello, World!"),
		},
		Dimensions:     openai.Int(int64(dim)),
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
	})

	require.NoError(t, err)
	require.Len(t, floatEmbedding.Data[0].Embedding, dim)

	// Get base64 encoding
	base64Embedding, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: model,
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String("Hello, World!"),
		},
		Dimensions:     openai.Int(int64(dim)),
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormatBase64,
	})

	require.NoError(t, err)
	require.Len(t, base64Embedding.Data, 1)

	// SDK doesn't decode base64 automatically, so extract from raw JSON
	rawJSON := base64Embedding.Data[0].RawJSON()
	require.NotEmpty(t, rawJSON)

	var rawData struct {
		Embedding string `json:"embedding"`
	}
	err = json.Unmarshal([]byte(rawJSON), &rawData)
	require.NoError(t, err)
	require.NotEmpty(t, rawData.Embedding, "base64 embedding should not be empty")

	// Decode base64 and verify dimensions
	decoded, err := decodeBase64Embedding(rawData.Embedding)
	require.NoError(t, err)
	require.Len(t, decoded, dim)

	// Values should be approximately equal (may have minor floating point differences)
	for i := 0; i < dim; i++ {
		require.InDelta(t, floatEmbedding.Data[0].Embedding[i], float64(decoded[i]), 1e-6,
			"embedding values at index %d should match", i)
	}
}

// Helper function to decode base64 embedding (if SDK returns raw string)
func decodeBase64Embedding(encoded string) ([]float32, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	floats := make([]float32, len(data)/4)
	for i := range floats {
		bits := binary.LittleEndian.Uint32(data[i*4:])
		floats[i] = math.Float32frombits(bits)
	}

	return floats, nil
}
