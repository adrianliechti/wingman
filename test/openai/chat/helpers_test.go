package chat_test

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

	openaiBody := withModel(body, h.ReferenceModel)
	wingmanBody := withModel(body, model.Name)

	openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/chat/completions", openaiBody)
	if err != nil {
		t.Fatalf("openai request failed: %v", err)
	}

	wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/chat/completions", wingmanBody)
	if err != nil {
		t.Fatalf("wingman request failed: %v", err)
	}

	if openaiResp.StatusCode != 200 {
		t.Fatalf("openai returned status %d: %s", openaiResp.StatusCode, string(openaiResp.RawBody))
	}
	if wingmanResp.StatusCode != 200 {
		t.Skipf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	return openaiResp, wingmanResp
}

func compareSSE(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()
	ctx := context.Background()

	openaiBody := withModel(body, h.ReferenceModel)
	openaiBody["stream"] = true

	wingmanBody := withModel(body, model.Name)
	wingmanBody["stream"] = true

	openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/chat/completions", openaiBody)
	if err != nil {
		t.Fatalf("openai SSE request failed: %v", err)
	}

	wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/chat/completions", wingmanBody)
	if err != nil {
		t.Fatalf("wingman SSE request failed: %v", err)
	}

	if len(openaiEvents) == 0 {
		t.Fatal("openai returned no SSE events")
	}
	if len(wingmanEvents) == 0 {
		t.Fatal("wingman returned no SSE events")
	}

	return openaiEvents, wingmanEvents
}
