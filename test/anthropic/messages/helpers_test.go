package messages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func withModel(body map[string]any, model string) map[string]any {
	m := make(map[string]any)
	maps.Copy(m, body)
	m["model"] = model
	return m
}

// postAnthropic sends a request with Anthropic-style headers (x-api-key).
func postAnthropic(t *testing.T, h *anthropic.Harness, ep harness.Endpoint, body map[string]any) *harness.RawResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), h.Client.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := ep.BaseURL + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", ep.APIKey)
	req.Header.Set("anthropic-version", anthropic.DefaultAnthropicVersion)

	// Add beta headers for features that require them
	var betas []string
	if _, ok := body["context_management"]; ok {
		betas = append(betas, "compact-2026-01-12")
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if tp, ok := tm["type"].(string); ok && strings.HasPrefix(tp, "computer") {
					betas = append(betas, "computer-use-2025-11-24")
				}
			}
		}
	}
	if len(betas) > 0 {
		req.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}

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

// postAnthropicSSE sends a streaming request with Anthropic-style headers.
func postAnthropicSSE(t *testing.T, h *anthropic.Harness, ep harness.Endpoint, body map[string]any) []*harness.SSEEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), h.Client.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := ep.BaseURL + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", ep.APIKey)
	req.Header.Set("anthropic-version", anthropic.DefaultAnthropicVersion)

	// Add beta headers for features that require them
	var betas []string
	if _, ok := body["context_management"]; ok {
		betas = append(betas, "compact-2026-01-12")
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if tp, ok := tm["type"].(string); ok && strings.HasPrefix(tp, "computer") {
					betas = append(betas, "computer-use-2025-11-24")
				}
			}
		}
	}
	if len(betas) > 0 {
		req.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}

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

func compareHTTP(t *testing.T, h *anthropic.Harness, model string, body map[string]any) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()

	anthropicBody := withModel(body, h.ReferenceModel)
	wingmanBody := withModel(body, model)

	anthropicResp := postAnthropic(t, h, h.Anthropic, anthropicBody)
	wingmanResp := postAnthropic(t, h, h.Wingman, wingmanBody)

	if anthropicResp.StatusCode != 200 {
		t.Fatalf("anthropic returned status %d: %s", anthropicResp.StatusCode, string(anthropicResp.RawBody))
	}
	if wingmanResp.StatusCode != 200 {
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	return anthropicResp, wingmanResp
}

func compareSSE(t *testing.T, h *anthropic.Harness, model string, body map[string]any) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()

	anthropicBody := withModel(body, h.ReferenceModel)
	anthropicBody["stream"] = true

	wingmanBody := withModel(body, model)
	wingmanBody["stream"] = true

	anthropicEvents := postAnthropicSSE(t, h, h.Anthropic, anthropicBody)
	wingmanEvents := postAnthropicSSE(t, h, h.Wingman, wingmanBody)

	if len(anthropicEvents) == 0 {
		t.Fatal("anthropic returned no SSE events")
	}
	if len(wingmanEvents) == 0 {
		t.Fatal("wingman returned no SSE events")
	}

	// Compare event type pattern
	anthropicTypes := sseEventTypes(anthropicEvents)
	wingmanTypes := sseEventTypes(wingmanEvents)

	if fmt.Sprint(anthropicTypes) != fmt.Sprint(wingmanTypes) {
		t.Errorf("SSE event type pattern mismatch:\n  anthropic: %v\n  wingman:   %v", anthropicTypes, wingmanTypes)
	}

	return anthropicEvents, wingmanEvents
}

func sseEventTypes(events []*harness.SSEEvent) []string {
	var types []string
	var prev string

	for _, e := range events {
		name := e.Event
		if name == "" {
			if t, ok := e.Data["type"].(string); ok {
				name = t
			}
		}

		// Skip ping events (Anthropic sends these, wingman doesn't)
		if name == "ping" {
			continue
		}

		// Collapse consecutive deltas
		if name != prev {
			types = append(types, name)
			prev = name
		}
	}

	return types
}
