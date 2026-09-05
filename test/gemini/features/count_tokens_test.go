package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

// TestCountTokensHTTP sends the official countTokens bodies — bare
// `contents`, and `generateContentRequest` carrying the system instruction
// and tools — to both endpoints. Wingman estimates rather than tokenizes, so
// values are not compared; the response shape and the modality breakdown
// are.
func TestCountTokensHTTP(t *testing.T) {
	h := gemini.New(t)

	contents := []map[string]any{
		{"role": "user", "parts": []map[string]any{{"text": "How many tokens is this sentence?"}}},
	}

	cases := []struct {
		name string
		body func(model string) map[string]any
	}{
		{
			name: "contents only",
			body: func(string) map[string]any {
				return map[string]any{"contents": contents}
			},
		},
		{
			name: "generateContentRequest with system instruction and tools",
			body: func(model string) map[string]any {
				return map[string]any{"generateContentRequest": map[string]any{
					"model":             "models/" + model,
					"contents":          contents,
					"systemInstruction": map[string]any{"parts": []map[string]any{{"text": "You are a careful assistant."}}},
					"tools":             []any{gemini.WeatherTool},
				}}
			},
		},
		{
			name: "snake_case singleton form",
			body: func(string) map[string]any {
				return map[string]any{"contents": map[string]any{"parts": map[string]any{"text": "Hello there, friend."}}}
			},
		},
	}

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					geminiResp := postCountTokens(t, h, h.Gemini, h.ReferenceModel, tc.body(h.ReferenceModel))
					if geminiResp.StatusCode != 200 {
						t.Fatalf("gemini returned status %d: %s", geminiResp.StatusCode, string(geminiResp.RawBody))
					}

					wingmanResp := postCountTokens(t, h, h.Wingman, model.Name, tc.body(model.Name))
					if wingmanResp.StatusCode != 200 {
						t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
					}

					requirePositiveTotalTokens(t, "gemini", geminiResp.Body)
					requirePositiveTotalTokens(t, "wingman", wingmanResp.Body)

					rules := map[string]harness.FieldRule{
						"totalTokens":                     harness.FieldNonEmpty,
						"promptTokensDetails.*.tokenCount": harness.FieldNonEmpty,
					}
					harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func postCountTokens(t *testing.T, h *gemini.Harness, ep harness.Endpoint, model string, body map[string]any) *harness.RawResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), h.Client.Timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := fmt.Sprintf("%s/models/%s:countTokens", ep.BaseURL, model)
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

func requirePositiveTotalTokens(t *testing.T, label string, body map[string]any) {
	t.Helper()

	v, ok := body["totalTokens"]
	if !ok {
		t.Fatalf("[%s] response missing totalTokens: %v", label, body)
	}

	switch n := v.(type) {
	case float64:
		if n <= 0 {
			t.Errorf("[%s] totalTokens = %v, want > 0", label, n)
		}
	case int:
		if n <= 0 {
			t.Errorf("[%s] totalTokens = %d, want > 0", label, n)
		}
	default:
		t.Errorf("[%s] totalTokens has unexpected type %T: %v", label, v, v)
	}
}
