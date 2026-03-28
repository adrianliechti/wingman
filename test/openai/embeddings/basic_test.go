package embeddings_test

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

const embeddingModel = "text-embedding-3-large"

func TestSingleHTTP(t *testing.T) {
	h := openai.New(t)

	body := map[string]any{
		"input": "Hello world",
	}

	openaiResp, wingmanResp := compareHTTP(t, h, openai.Model{Name: embeddingModel}, body)

	requireEmbeddings(t, "openai", openaiResp.Body, 1)
	requireEmbeddings(t, "wingman", wingmanResp.Body, 1)

	rules := openai.DefaultEmbeddingResponseRules()
	harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func TestBatchHTTP(t *testing.T) {
	h := openai.New(t)

	body := map[string]any{
		"input": []string{"Hello", "World", "Test"},
	}

	openaiResp, wingmanResp := compareHTTP(t, h, openai.Model{Name: embeddingModel}, body)

	requireEmbeddings(t, "openai", openaiResp.Body, 3)
	requireEmbeddings(t, "wingman", wingmanResp.Body, 3)

	rules := openai.DefaultEmbeddingResponseRules()
	harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func TestDimensionsHTTP(t *testing.T) {
	h := openai.New(t)

	body := map[string]any{
		"input":      "Hello world",
		"dimensions": 256,
	}

	openaiResp, wingmanResp := compareHTTP(t, h, openai.Model{Name: embeddingModel}, body)

	requireEmbeddings(t, "openai", openaiResp.Body, 1)
	requireEmbeddings(t, "wingman", wingmanResp.Body, 1)

	requireEmbeddingDimensions(t, "openai", openaiResp.Body, 256)
	requireEmbeddingDimensions(t, "wingman", wingmanResp.Body, 256)

	rules := openai.DefaultEmbeddingResponseRules()
	harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func TestBase64HTTP(t *testing.T) {
	h := openai.New(t)

	body := map[string]any{
		"input":           "Hello world",
		"encoding_format": "base64",
	}

	openaiResp, wingmanResp := compareHTTP(t, h, openai.Model{Name: embeddingModel}, body)

	requireEmbeddings(t, "openai", openaiResp.Body, 1)
	requireEmbeddings(t, "wingman", wingmanResp.Body, 1)

	// Verify base64 data can be decoded to floats
	requireBase64Embedding(t, "openai", openaiResp.Body)
	requireBase64Embedding(t, "wingman", wingmanResp.Body)

	rules := openai.DefaultEmbeddingResponseRules()
	harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func requireEmbeddings(t *testing.T, label string, body map[string]any, count int) {
	t.Helper()

	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("[%s] data is not an array", label)
	}

	if len(data) != count {
		t.Fatalf("[%s] expected %d embeddings, got %d", label, count, len(data))
	}

	for i, item := range data {
		obj, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("[%s] data[%d] is not an object", label, i)
		}

		if obj["object"] != "embedding" {
			t.Errorf("[%s] data[%d].object = %v, want 'embedding'", label, i, obj["object"])
		}
	}
}

func requireEmbeddingDimensions(t *testing.T, label string, body map[string]any, dims int) {
	t.Helper()

	data := body["data"].([]any)
	obj := data[0].(map[string]any)
	embedding, ok := obj["embedding"].([]any)

	if !ok {
		t.Fatalf("[%s] embedding is not a float array", label)
	}

	if len(embedding) != dims {
		t.Errorf("[%s] expected %d dimensions, got %d", label, dims, len(embedding))
	}
}

func requireBase64Embedding(t *testing.T, label string, body map[string]any) {
	t.Helper()

	data := body["data"].([]any)
	obj := data[0].(map[string]any)
	b64, ok := obj["embedding"].(string)

	if !ok {
		t.Fatalf("[%s] embedding is not a base64 string", label)
	}

	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("[%s] failed to decode base64: %v", label, err)
	}

	if len(decoded)%4 != 0 {
		t.Fatalf("[%s] decoded bytes length %d not divisible by 4 (float32)", label, len(decoded))
	}

	// Verify first float is a reasonable number
	bits := binary.LittleEndian.Uint32(decoded[:4])
	val := math.Float32frombits(bits)

	if math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
		t.Errorf("[%s] first float is NaN or Inf: %v", label, val)
	}
}
