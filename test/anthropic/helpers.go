package anthropic

import (
	"bytes"
	"context"
	"encoding/json/v2"
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
	return postMessagesSSE(t, h, ep, body, false)
}

// PostMessagesSSEAllowRejection is for compatibility probes whose reference
// may reject a request before opening the stream. Other HTTP failures remain
// test failures; a 400 is exposed as an error event for the caller to inspect.
func PostMessagesSSEAllowRejection(t *testing.T, h *Harness, ep harness.Endpoint, body map[string]any) []*harness.SSEEvent {
	t.Helper()
	return postMessagesSSE(t, h, ep, body, true)
}

func postMessagesSSE(t *testing.T, h *Harness, ep harness.Endpoint, body map[string]any, allowRejection bool) []*harness.SSEEvent {
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

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		if allowRejection && resp.StatusCode == http.StatusBadRequest {
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatalf("invalid rejection response from %s: %v", ep.Name, err)
			}
			return []*harness.SSEEvent{{Event: "error", Data: data, Raw: string(raw)}}
		}
		t.Fatalf("%s returned status %d: %s", ep.Name, resp.StatusCode, raw)
	}

	events, err := harness.ParseSSE(resp.Body)
	if err != nil {
		t.Fatalf("parse SSE from %s: %v", ep.Name, err)
	}

	return events
}

// betaHeaders returns the beta headers required by features used in the body.
func betaHeaders(body map[string]any) []string {
	var betas []string
	if thinking, ok := body["thinking"].(map[string]any); ok && thinking["display"] == "updates" {
		betas = append(betas, "thinking-display-updates-2026-08-18")
	}
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
	ValidateMessageStream(t, anthropicEvents)
	ValidateMessageStream(t, wingmanEvents)

	anthropicTypes := SSEEventTypes(anthropicEvents)
	wingmanTypes := SSEEventTypes(wingmanEvents)

	if fmt.Sprint(anthropicTypes) != fmt.Sprint(wingmanTypes) {
		t.Errorf("SSE event type pattern mismatch:\n  anthropic: %v\n  wingman:   %v", anthropicTypes, wingmanTypes)
	}

	return anthropicEvents, wingmanEvents
}

// SSEEventTypes compares the non-thinking event pattern. Adaptive models
// choose whether to think; dedicated tests check their signed output.
func SSEEventTypes(events []*harness.SSEEvent) []string {
	var types []string
	var prev string

	for _, e := range WithoutThinking(events) {
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

// WithoutThinking lets schema comparisons match text/tool events even when
// only one model produces thinking. The original events remain intact.
func WithoutThinking(events []*harness.SSEEvent) []*harness.SSEEvent {
	var result []*harness.SSEEvent
	thinking := map[float64]bool{}
	for _, event := range events {
		index, indexed := event.Data["index"].(float64)
		if block, ok := event.Data["content_block"].(map[string]any); ok && indexed {
			if block["type"] == "thinking" || block["type"] == "redacted_thinking" {
				thinking[index] = true
			}
		}
		if !indexed || !thinking[index] {
			result = append(result, event)
		}
	}
	return result
}

// ValidateMessageStream checks real block boundaries before comparisons
// ignore differences in chunk counts and optional thinking blocks.
func ValidateMessageStream(t *testing.T, events []*harness.SSEEvent) {
	t.Helper()
	if err := messageStreamError(events); err != nil {
		t.Fatal(err)
	}
}

func messageStreamError(events []*harness.SSEEvent) error {
	open, seen := map[float64]bool{}, map[float64]bool{}
	started, stopped := false, false
	for _, event := range events {
		index, indexed := event.Data["index"].(float64)
		if strings.HasPrefix(event.Event, "content_block_") && (!indexed || index < 0 || index != float64(int(index))) {
			return fmt.Errorf("%s has invalid index: %v", event.Event, event.Data["index"])
		}
		switch event.Event {
		case "error":
			return fmt.Errorf("stream error: %v", event.Data)
		case "message_start":
			if started {
				return fmt.Errorf("duplicate message_start")
			}
			started = true
		case "content_block_start":
			if !started || stopped || seen[index] {
				return fmt.Errorf("invalid content_block_start at %v", index)
			}
			open[index], seen[index] = true, true
		case "content_block_delta", "content_block_stop":
			if !open[index] {
				return fmt.Errorf("%s outside block %v", event.Event, index)
			}
			if event.Event == "content_block_stop" {
				delete(open, index)
			}
		case "message_delta":
			if !started || stopped || len(open) != 0 {
				return fmt.Errorf("message_delta with unfinished blocks or invalid lifecycle")
			}
		case "message_stop":
			if !started || stopped || len(open) != 0 {
				return fmt.Errorf("message_stop with unfinished blocks or invalid lifecycle")
			}
			stopped = true
		}
	}
	if !stopped {
		return fmt.Errorf("stream did not finish")
	}
	return nil
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
