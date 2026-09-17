package anthropic

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

	"github.com/adrianliechti/wingman/test/harness"
)

var WeatherTool = map[string]any{
	"name":        "get_weather",
	"description": "Get the current weather for a location",
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The city and country",
			},
		},
		"required": []string{"location"},
	},
}

func (h *Harness) SkipUnlessConfigured(t *testing.T, model string) {
	t.Helper()
	harness.SkipUnlessConfigured(t, h.Wingman.BaseURL, h.Wingman.APIKey, model)
}

func WithModel(body map[string]any, model string) map[string]any {
	m := make(map[string]any)
	maps.Copy(m, body)
	m["model"] = model
	return m
}

// PostMessages sends a request with Anthropic-style headers (x-api-key).
func PostMessages(t *testing.T, h *Harness, ep harness.Endpoint, body map[string]any) *harness.RawResponse {
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
	req.Header.Set("anthropic-version", DefaultAnthropicVersion)

	if betas := betaHeaders(body); len(betas) > 0 {
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

// PostMessagesSSE sends a streaming request with Anthropic-style headers.
func PostMessagesSSE(t *testing.T, h *Harness, ep harness.Endpoint, body map[string]any) []*harness.SSEEvent {
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
	req.Header.Set("anthropic-version", DefaultAnthropicVersion)

	if betas := betaHeaders(body); len(betas) > 0 {
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

// betaHeaders returns the beta headers required by features used in the body.
func betaHeaders(body map[string]any) []string {
	var betas []string
	// Signed compaction also needs the beta on every continuation request.
	data, _ := json.Marshal(body["messages"])
	if body["compaction"] != nil || strings.Contains(string(data), `"type":"compaction"`) && strings.Contains(string(data), `"signature"`) {
		betas = append(betas, "compact-2026-09-04")
	}

	if _, ok := body["context_management"]; ok {
		betas = append(betas, "compact-2026-01-12")
	}

	if tools, ok := body["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if tp, ok := tm["type"].(string); ok {
					if strings.HasPrefix(tp, "computer") {
						betas = append(betas, "computer-use-2025-11-24")
					}
					if strings.HasPrefix(tp, "web_fetch") {
						betas = append(betas, "web-fetch-2025-09-10")
					}
				}
			}
		}
	}

	return betas
}

func CompareHTTP(t *testing.T, h *Harness, model string, body map[string]any) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()

	h.SkipUnlessConfigured(t, model)

	anthropicBody := WithModel(body, h.ReferenceModel)
	wingmanBody := WithModel(body, model)

	anthropicResp := PostMessages(t, h, h.Anthropic, anthropicBody)
	wingmanResp := PostMessages(t, h, h.Wingman, wingmanBody)

	if anthropicResp.StatusCode != 200 {
		t.Fatalf("anthropic returned status %d: %s", anthropicResp.StatusCode, string(anthropicResp.RawBody))
	}
	if wingmanResp.StatusCode != 200 {
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}

	return anthropicResp, wingmanResp
}

func CompareSSE(t *testing.T, h *Harness, model string, body map[string]any) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()

	h.SkipUnlessConfigured(t, model)

	anthropicBody := WithModel(body, h.ReferenceModel)
	anthropicBody["stream"] = true

	wingmanBody := WithModel(body, model)
	wingmanBody["stream"] = true

	anthropicEvents := PostMessagesSSE(t, h, h.Anthropic, anthropicBody)
	wingmanEvents := PostMessagesSSE(t, h, h.Wingman, wingmanBody)

	if len(anthropicEvents) == 0 {
		t.Fatal("anthropic returned no SSE events")
	}
	if len(wingmanEvents) == 0 {
		t.Fatal("wingman returned no SSE events")
	}

	anthropicTypes := SSEEventTypes(anthropicEvents)
	wingmanTypes := SSEEventTypes(wingmanEvents)

	if fmt.Sprint(anthropicTypes) != fmt.Sprint(wingmanTypes) {
		t.Errorf("SSE event type pattern mismatch:\n  anthropic: %v\n  wingman:   %v", anthropicTypes, wingmanTypes)
	}

	return anthropicEvents, wingmanEvents
}

// SSEEventTypes collapses an event stream into its type pattern. Ping events
// are skipped and consecutive events of the same type are merged.
func SSEEventTypes(events []*harness.SSEEvent) []string {
	var types []string
	var prev string

	for _, e := range events {
		name := e.Event
		if name == "" {
			if t, ok := e.Data["type"].(string); ok {
				name = t
			}
		}

		if name == "ping" {
			continue
		}

		if name != prev {
			types = append(types, name)
			prev = name
		}
	}

	return types
}

func RequireTextContent(t *testing.T, label string, body map[string]any) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}

	for _, block := range content {
		obj, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "text" {
			text, _ := obj["text"].(string)
			if text != "" {
				return
			}
		}
	}

	t.Fatalf("[%s] no text content block found", label)
}
