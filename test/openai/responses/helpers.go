package responses

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

// WithModel returns a shallow copy of body with the model field set.
func WithModel(body map[string]any, model string) map[string]any {
	m := make(map[string]any)
	maps.Copy(m, body)
	m["model"] = model
	// Disable the default reference model's optional reasoning in basic
	// comparisons. Reasoning scenarios supply their own configuration.
	if _, explicit := m["reasoning"]; !explicit && strings.HasPrefix(model, "gpt-6-luna") {
		m["reasoning"] = map[string]any{"effort": "none"}
	}
	return m
}

// CompareHTTP sends the same request to OpenAI (reference model) and wingman (actual model),
// then compares the response structures.
func CompareHTTP(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) (*harness.RawResponse, *harness.RawResponse) {
	t.Helper()
	h.SkipUnlessConfigured(t, model.Name)
	ctx := context.Background()

	openaiBody := WithModel(body, h.ReferenceModel)
	wingmanBody := WithModel(body, model.Name)

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
		t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
	}
	if body["reasoning"] == nil {
		openaiResp.Body = withoutReasoningOutput(openaiResp.Body)
		wingmanResp.Body = withoutReasoningOutput(wingmanResp.Body)
	}

	return openaiResp, wingmanResp
}

// CompareSSE sends the same streaming request to OpenAI and wingman, then compares event structures.
func CompareSSE(t *testing.T, h *openai.Harness, model openai.Model, body map[string]any) ([]*harness.SSEEvent, []*harness.SSEEvent) {
	t.Helper()
	h.SkipUnlessConfigured(t, model.Name)
	ctx := context.Background()

	openaiBody := WithModel(body, h.ReferenceModel)
	openaiBody["stream"] = true

	wingmanBody := WithModel(body, model.Name)
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
	if body["reasoning"] == nil {
		openaiEvents = withoutReasoningEvents(openaiEvents)
		wingmanEvents = withoutReasoningEvents(wingmanEvents)
	}

	harness.CompareSSEEventPattern(t, comparableSSEEvents(openaiEvents), comparableSSEEvents(wingmanEvents))

	return openaiEvents, wingmanEvents
}

// Some models always reason, even at their lowest effort. In scenarios that
// do not request reasoning, compare the remaining output and its event indices.
// Explicit reasoning scenarios keep their full output for validation.
func withoutReasoningOutput(body map[string]any) map[string]any {
	result := maps.Clone(body)
	if output, ok := body["output"].([]any); ok {
		result["output"] = slices.DeleteFunc(slices.Clone(output), func(raw any) bool {
			item, _ := raw.(map[string]any)
			return item["type"] == "reasoning"
		})
	}
	return result
}

func withoutReasoningEvents(events []*harness.SSEEvent) []*harness.SSEEvent {
	indices := map[float64]bool{}
	for _, event := range events {
		if item, ok := event.Data["item"].(map[string]any); ok && item["type"] == "reasoning" {
			index, ok := event.Data["output_index"].(float64)
			if ok {
				indices[index] = true
			}
		}
	}
	var result []*harness.SSEEvent
	for _, event := range events {
		copy := *event
		copy.Data = maps.Clone(event.Data)
		if index, ok := event.Data["output_index"].(float64); ok {
			if indices[index] {
				continue
			}
			adjusted := index
			for reasoningIndex := range indices {
				if reasoningIndex < index {
					adjusted--
				}
			}
			copy.Data["output_index"] = adjusted
		}
		if response, ok := event.Data["response"].(map[string]any); ok {
			copy.Data["response"] = withoutReasoningOutput(response)
		}
		result = append(result, &copy)
	}
	return result
}

func comparableSSEEvents(events []*harness.SSEEvent) []*harness.SSEEvent {
	result := make([]*harness.SSEEvent, 0, len(events))
	for _, e := range events {
		switch eventType(e) {
		case "response.reasoning_summary_part.added",
			"response.reasoning_summary_text.delta",
			"response.reasoning_summary_text.done",
			"response.reasoning_summary_part.done":
			continue
		}
		result = append(result, e)
	}
	return result
}

// RequireMessageOutput checks that the response contains a message output item.
func RequireMessageOutput(t *testing.T, label string, body map[string]any) {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "message" {
			return
		}
	}

	t.Fatalf("[%s] no message output item found", label)
}

func eventType(e *harness.SSEEvent) string {
	if e == nil {
		return ""
	}
	if e.Event != "" {
		return e.Event
	}
	if e.Data != nil {
		if typ, ok := e.Data["type"].(string); ok {
			return typ
		}
	}
	return ""
}
