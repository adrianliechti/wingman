package responses_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
)

// TestInvalidContentType verifies the round-trip contract when a request
// contains a file with a content type the provider doesn't support — both
// in a user message and inside a function_call_output. Whichever layer
// rejects (wingman's strict providers vs. the upstream catching forwarded
// content), the response must be 4xx and reference the offending mime.
//
// Also verifies that the file_data field accepts both a raw base64 string
// and a "data:" URL — OpenAI's wire allows either form.
func TestInvalidContentType(t *testing.T) {
	h := openai.New(t)

	// Raw base64 (3 zero bytes). The filename extension drives mime
	// detection on the wingman side (.mp3 → audio/mpeg).
	const audioBase64 = "AAAA"
	const audioDataURL = "data:audio/mpeg;base64,AAAA"

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			name: "audio in user message (raw base64)",
			body: map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "describe this audio"},
							{
								"type":      "input_file",
								"filename":  "clip.mp3",
								"file_data": audioBase64,
							},
						},
					},
				},
			},
		},
		{
			name: "audio in user message (data url)",
			body: map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "describe this audio"},
							{
								"type":      "input_file",
								"filename":  "clip.mp3",
								"file_data": audioDataURL,
							},
						},
					},
				},
			},
		},
		{
			name: "audio in function_call_output",
			body: map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": "What did the recorder return?",
					},
					{
						"type":      "function_call",
						"call_id":   "c_audio",
						"name":      "record",
						"arguments": "{}",
					},
					{
						"type":    "function_call_output",
						"call_id": "c_audio",
						"output": []map[string]any{
							{"type": "input_text", "text": "here's the recording"},
							{
								"type":      "input_file",
								"filename":  "clip.mp3",
								"file_data": audioDataURL,
							},
						},
					},
				},
			},
		},
	}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx := context.Background()

					wingmanBody := withModel(tc.body, model.Name)
					wingmanResp, err := h.Client.Post(ctx, h.Wingman, "/responses", wingmanBody)
					if err != nil {
						t.Fatalf("wingman request: %v", err)
					}

					if wingmanResp.StatusCode < 400 || wingmanResp.StatusCode >= 500 {
						t.Fatalf("wingman: expected 4xx, got %d: %s",
							wingmanResp.StatusCode, string(wingmanResp.RawBody))
					}

					errMsg := extractError(wingmanResp.RawBody)
					if !strings.Contains(strings.ToLower(errMsg), "audio/mpeg") {
						t.Errorf("error should reference the offending mime; got %q", errMsg)
					}

					// Cross-provider comparison: also post to OpenAI directly
					// and log both error shapes so any divergence is visible.
					openaiBody := withModel(tc.body, h.ReferenceModel)
					openaiResp, err := h.Client.Post(ctx, h.OpenAI, "/responses", openaiBody)
					if err != nil {
						t.Logf("openai request error: %v (skipping comparison)", err)
						return
					}

					t.Logf("status — wingman: %d, openai: %d", wingmanResp.StatusCode, openaiResp.StatusCode)
					if openaiResp.StatusCode >= 400 {
						t.Logf("openai error: %s", extractError(openaiResp.RawBody))
					}
				})
			}
		})
	}
}

// extractError pulls the first "message" string out of a typical JSON error body
// (handles both flat {"error": "msg"} and nested {"error": {"message": "msg"}} shapes).
func extractError(body []byte) string {
	var flat struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &flat); err == nil && flat.Error != "" {
		return flat.Error
	}

	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &nested); err == nil && nested.Error.Message != "" {
		return nested.Error.Message
	}

	return string(body)
}
