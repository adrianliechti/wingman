package responses_test

import (
	"context"
	"maps"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

// withModel returns a shallow copy of body with the model field set.
func withModel(body map[string]any, model string) map[string]any {
	m := make(map[string]any)
	maps.Copy(m, body)
	m["model"] = model
	return m
}

// compareHTTP sends the same request to OpenAI (reference model) and wingman (actual model),
// then compares the response structures.
func compareHTTP(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()
	ctx := context.Background()

	openaiBody := withModel(body, h.ReferenceModel)
	wingmanBody := withModel(body, model.Name)

	openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/responses", openaiBody)
	if err != nil {
		t.Fatalf("openai request failed: %v", err)
	}

	wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/responses", wingmanBody)
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

// compareSSE sends the same streaming request to OpenAI and wingman, then compares event structures.
func compareSSE(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()
	ctx := context.Background()

	openaiBody := withModel(body, h.ReferenceModel)
	openaiBody["stream"] = true

	wingmanBody := withModel(body, model.Name)
	wingmanBody["stream"] = true

	openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", openaiBody)
	if err != nil {
		t.Fatalf("openai SSE request failed: %v", err)
	}

	wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", wingmanBody)
	if err != nil {
		t.Fatalf("wingman SSE request failed: %v", err)
	}

	if len(openaiEvents) == 0 {
		t.Fatal("openai returned no SSE events")
	}
	if len(wingmanEvents) == 0 {
		t.Fatal("wingman returned no SSE events")
	}

	harness.CompareSSEEventPattern(t, openaiEvents, wingmanEvents)

	return openaiEvents, wingmanEvents
}
