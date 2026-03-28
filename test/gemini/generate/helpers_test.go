package generate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

func withModel(body map[string]any, model string) map[string]any {
	m := make(map[string]any)
	maps.Copy(m, body)
	return m
}

// postGemini sends a request with Gemini-style auth (API key in query param or header).
func postGemini(t *testing.T, h *gemini.Harness, ep harness.Endpoint, model string, body map[string]any) *harness.RawResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), h.Client.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", ep.BaseURL, model)

	// Gemini API uses key= query param, wingman uses x-goog-api-key header
	if ep.Name == "gemini" {
		url += "?key=" + ep.APIKey
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", ep.APIKey)

	resp, err := h.Client.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do request to %s: %v", ep.Name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response from %s: %v", ep.Name, err)
	}

	result := &harness.RawResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		RawBody:    raw,
	}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result.Body); err != nil {
			t.Fatalf("unmarshal response from %s: %v\nbody: %s", ep.Name, err, string(raw))
		}
	}

	return result
}

// postGeminiSSE sends a streaming request with alt=sse.
func postGeminiSSE(t *testing.T, h *gemini.Harness, ep harness.Endpoint, model string, body map[string]any) []*harness.SSEEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), h.Client.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", ep.BaseURL, model)

	if ep.Name == "gemini" {
		url += "&key=" + ep.APIKey
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", ep.APIKey)

	resp, err := h.Client.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do request to %s: %v", ep.Name, err)
	}
	defer resp.Body.Close()

	events, err := harness.ParseSSE(resp.Body)
	if err != nil {
		t.Fatalf("parse SSE from %s: %v", ep.Name, err)
	}

	return events
}

func compareHTTP(t *testing.T, h *gemini.Harness, model string, body map[string]any) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()

	geminiResp := postGemini(t, h, h.Gemini, h.ReferenceModel, body)
	wingmanResp := postGemini(t, h, h.Wingman, model, body)

	if geminiResp.StatusCode != 200 {
		t.Fatalf("gemini returned status %d: %s", geminiResp.StatusCode, string(geminiResp.RawBody))
	}
	if wingmanResp.StatusCode != 200 {
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	return geminiResp, wingmanResp
}

func compareSSE(t *testing.T, h *gemini.Harness, model string, body map[string]any) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()

	geminiEvents := postGeminiSSE(t, h, h.Gemini, h.ReferenceModel, body)
	wingmanEvents := postGeminiSSE(t, h, h.Wingman, model, body)

	if len(geminiEvents) == 0 {
		t.Fatal("gemini returned no SSE events")
	}
	if len(wingmanEvents) == 0 {
		t.Fatal("wingman returned no SSE events")
	}

	return geminiEvents, wingmanEvents
}
