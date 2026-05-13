package messages_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

// TestInvalidContentType verifies that the Anthropic /v1/messages endpoint
// rejects unsupported file content types with 4xx and an informative error,
// both in a user message and inside a tool_result. Whichever layer
// rejects (the strict Anthropic/Bedrock providers reject at wingman; the
// lenient OpenAI Responder forwards to upstream which then catches it),
// the round-trip must end in 4xx that references the offending mime.
func TestInvalidContentType(t *testing.T) {
	h := anthropic.New(t)

	const audioBase64 = "AAAA" // 3 zero bytes

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			name: "audio in user message",
			body: map[string]any{
				"max_tokens": 100,
				"messages": []map[string]any{
					{
						"role": "user",
						"content": []map[string]any{
							{"type": "text", "text": "describe this audio"},
							{
								"type": "image",
								"source": map[string]any{
									"type":       "base64",
									"media_type": "audio/mpeg",
									"data":       audioBase64,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "audio in tool_result",
			body: map[string]any{
				"max_tokens": 100,
				"tools": []map[string]any{
					{
						"name":        "record",
						"description": "record audio",
						"input_schema": map[string]any{
							"type":       "object",
							"properties": map[string]any{},
						},
					},
				},
				"messages": []map[string]any{
					{"role": "user", "content": "play the recording"},
					{
						"role": "assistant",
						"content": []map[string]any{
							{
								"type":  "tool_use",
								"id":    "toolu_audio",
								"name":  "record",
								"input": map[string]any{},
							},
						},
					},
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type":         "tool_result",
								"tool_use_id":  "toolu_audio",
								"content": []map[string]any{
									{"type": "text", "text": "here:"},
									{
										"type": "image",
										"source": map[string]any{
											"type":       "base64",
											"media_type": "audio/mpeg",
											"data":       audioBase64,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					body := withModel(tc.body, model.Name)
					wingmanResp := postAnthropic(t, h, h.Wingman, body)

					if wingmanResp.StatusCode < 400 || wingmanResp.StatusCode >= 500 {
						t.Fatalf("wingman: expected 4xx, got %d: %s",
							wingmanResp.StatusCode, string(wingmanResp.RawBody))
					}

					errMsg := extractAnthropicError(wingmanResp.RawBody)
					if !strings.Contains(strings.ToLower(errMsg), "audio/mpeg") {
						t.Errorf("error should reference the offending mime; got %q", errMsg)
					}

					// Cross-provider comparison.
					anthropicBody := withModel(tc.body, h.ReferenceModel)
					anthropicResp := postAnthropic(t, h, h.Anthropic, anthropicBody)
					t.Logf("status — wingman: %d, anthropic: %d", wingmanResp.StatusCode, anthropicResp.StatusCode)
					if anthropicResp.StatusCode >= 400 {
						t.Logf("anthropic error: %s", extractAnthropicError(anthropicResp.RawBody))
					}
				})
			}
		})
	}
}

func extractAnthropicError(body []byte) string {
	var resp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err == nil && resp.Error.Message != "" {
		return resp.Error.Message
	}
	return string(body)
}
