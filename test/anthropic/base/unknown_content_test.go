package base_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

// TestUnknownContentRejected verifies that unknown content blocks, unknown
// tool_result parts, unsupported document sources and toolsets are rejected
// with a 400 that names the field, on the reference and through Wingman on
// every backend, instead of being dropped from the prompt.
func TestUnknownContentRejected(t *testing.T) {
	h := anthropic.New(t)

	cases := []struct {
		name   string
		body   map[string]any
		prefix string
	}{
		{
			name: "unknown block",
			body: map[string]any{
				"max_tokens": 32,
				"messages": []map[string]any{{"role": "user", "content": []map[string]any{
					{"type": "text", "text": "hi"},
					{"type": "bogus_block", "text": "hi"},
				}}},
			},
			prefix: "messages.0.content.1: Input tag 'bogus_block' found using 'type'",
		},
		{
			name: "unknown tool_result part",
			body: map[string]any{
				"max_tokens": 32,
				"tools":      []any{anthropic.WeatherTool},
				"messages": []map[string]any{
					{"role": "user", "content": "weather?"},
					{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": map[string]any{"location": "London"}}}},
					{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "toolu_1", "content": []map[string]any{
						{"type": "text", "text": "sunny"},
						{"type": "bogus_part"},
					}}}},
				},
			},
			prefix: "messages.2.content.0.tool_result.content.1: Input tag 'bogus_part' found using 'type'",
		},
		{
			name: "toolset",
			body: map[string]any{
				"max_tokens": 32,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
				"tools":      []map[string]any{{"type": "computer_toolset_20260801", "name": "computer"}},
			},
			prefix: "tools.0",
		},
	}

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					wingmanResp := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(tc.body, model.Name))
					if wingmanResp.StatusCode != 400 {
						t.Fatalf("wingman: expected 400, got %d: %s", wingmanResp.StatusCode, string(wingmanResp.RawBody))
					}
					if msg := extractAnthropicError(wingmanResp.RawBody); !strings.HasPrefix(msg, tc.prefix) {
						t.Errorf("wingman error %q does not start with %q", msg, tc.prefix)
					}

					anthropicResp := anthropic.PostMessages(t, h, h.Anthropic, anthropic.WithModel(tc.body, h.ReferenceModel))
					if anthropicResp.StatusCode != 400 {
						t.Fatalf("anthropic: expected 400, got %d: %s", anthropicResp.StatusCode, string(anthropicResp.RawBody))
					}
					if msg := extractAnthropicError(anthropicResp.RawBody); !strings.HasPrefix(msg, tc.prefix) {
						t.Errorf("anthropic error %q does not start with %q", msg, tc.prefix)
					}
				})
			}
		})
	}
}
