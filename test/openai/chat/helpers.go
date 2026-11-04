package chat

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func WithModel(body map[string]any, model string) map[string]any {
	m := make(map[string]any)
	maps.Copy(m, body)
	m["model"] = model
	// GPT-6 defaults to reasoning, which its Chat Completions endpoint rejects
	// with function tools. Explicit reasoning scenarios retain their setting.
	if _, explicit := m["reasoning_effort"]; !explicit && m["tools"] != nil && strings.HasPrefix(model, "gpt-6") {
		m["reasoning_effort"] = "none"
	}
	return m
}

func CompareHTTP(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()
	h.SkipUnlessConfigured(t, model.Name)
	ctx := context.Background()

	openaiBody := WithModel(body, h.ReferenceModel)
	wingmanBody := WithModel(body, model.Name)

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
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	return openaiResp, wingmanResp
}

func CompareSSE(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()
	h.SkipUnlessConfigured(t, model.Name)
	ctx := context.Background()

	openaiBody := WithModel(body, h.ReferenceModel)
	openaiBody["stream"] = true

	wingmanBody := WithModel(body, model.Name)
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
