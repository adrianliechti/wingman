package generate_test

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

// TestCountTokensHTTP exercises the /v1beta/models/{model}:countTokens
// endpoint. Wingman's count is heuristic (chars/4) and won't match the
// upstream tokenizer exactly, so we don't compare values — we only
// assert both endpoints return a positive totalTokens for the same
// payload. That catches the common failure modes (404, 500, missing
// field, zero-count regression).
//
// Upstream Gemini's countTokens rejects systemInstruction/tools at the
// top level — those must be nested inside generateContentRequest.
// Wingman's handler currently only accepts the flat top-level shape.
// We send the right shape to each endpoint so both succeed on the same
// logical payload.
func TestCountTokensHTTP(t *testing.T) {
	h := gemini.New(t)

	cases := []struct {
		name         string
		contents     []map[string]any
		system       map[string]any
		tools        []any
	}{
		{
			name: "contents only",
			contents: []map[string]any{
				{"role": "user", "parts": []map[string]any{{"text": "How many tokens is this sentence?"}}},
			},
		},
		{
			name: "contents with system instruction",
			system: map[string]any{
				"parts": []map[string]any{{"text": "You are a careful assistant."}},
			},
			contents: []map[string]any{
				{"role": "user", "parts": []map[string]any{{"text": "Hello there, friend."}}},
			},
		},
		{
			name: "contents with tools",
			contents: []map[string]any{
				{"role": "user", "parts": []map[string]any{{"text": "What's the weather?"}}},
			},
			tools: []any{weatherTool},
		},
	}

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					upstreamBody := buildCountTokensBody(tc.contents, tc.system, tc.tools, h.ReferenceModel)
					wingmanBody := buildCountTokensBody(tc.contents, tc.system, tc.tools, "")

					geminiResp := postCountTokens(t, h, h.Gemini, h.ReferenceModel, upstreamBody)
					if geminiResp.StatusCode != 200 {
						t.Fatalf("gemini returned status %d: %s", geminiResp.StatusCode, string(geminiResp.RawBody))
					}

					wingmanResp := postCountTokens(t, h, h.Wingman, model.Name, wingmanBody)
					if wingmanResp.StatusCode != 200 {
						t.Fatalf("wingman returned status %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
					}

					requirePositiveTotalTokens(t, "gemini", geminiResp.Body)
					requirePositiveTotalTokens(t, "wingman", wingmanResp.Body)
				})
			}
		})
	}
}

// buildCountTokensBody returns a request body for /v1beta/models/{model}:countTokens.
//
// When wrapModel is non-empty (the upstream-Gemini case with non-flat
// fields), the body is wrapped in generateContentRequest with a nested
// model — upstream rejects systemInstruction/tools at the top level and
// also requires generateContentRequest.model when the wrapper is used.
//
// When wrapModel is empty (the Wingman case), the flat shape is used —
// Wingman's handler_tokens.go only accepts {contents, systemInstruction, tools}.
func buildCountTokensBody(contents []map[string]any, system map[string]any, tools []any, wrapModel string) map[string]any {
	if wrapModel == "" || (system == nil && tools == nil) {
		body := map[string]any{"contents": contents}
		if system != nil {
			body["systemInstruction"] = system
		}
		if tools != nil {
			body["tools"] = tools
		}
		return body
	}

	inner := map[string]any{
		"model":    "models/" + wrapModel,
		"contents": contents,
	}
	if system != nil {
		inner["systemInstruction"] = system
	}
	if tools != nil {
		inner["tools"] = tools
	}
	return map[string]any{"generateContentRequest": inner}
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
