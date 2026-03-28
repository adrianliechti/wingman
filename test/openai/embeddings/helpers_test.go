package embeddings_test

import (
	"context"
	"maps"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func withModel(body map[string]any, model string) map[string]any {
	m := make(map[string]any)
	maps.Copy(m, body)
	m["model"] = model
	return m
}

func compareHTTP(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()
	ctx := context.Background()

	openaiBody := withModel(body, model.Name)
	wingmanBody := withModel(body, model.Name)

	openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/embeddings", openaiBody)
	if err != nil {
		t.Fatalf("openai request failed: %v", err)
	}

	wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/embeddings", wingmanBody)
	if err != nil {
		t.Fatalf("wingman request failed: %v", err)
	}

	if openaiResp.StatusCode != 200 {
		t.Fatalf("openai returned status %d: %s", openaiResp.StatusCode, string(openaiResp.RawBody))
	}
	if wingmanResp.StatusCode != 200 {
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	return openaiResp, wingmanResp
}
