package chat

import "testing"

func TestWithModelToolReasoning(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		body        map[string]any
		want        any
	}{
		{"default tools", "gpt-6-luna", map[string]any{"tools": []any{}}, "none"},
		{"explicit reasoning", "gpt-6-luna", map[string]any{"tools": []any{}, "reasoning_effort": "high"}, "high"},
		{"text only", "gpt-6-luna", map[string]any{}, nil},
		{"other provider", "claude-sonnet-5-5", map[string]any{"tools": []any{}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.body["reasoning_effort"]
			got := WithModel(tc.body, tc.model)
			if got["reasoning_effort"] != tc.want || got["model"] != tc.model {
				t.Fatalf("unexpected request: %v", got)
			}
			if tc.body["reasoning_effort"] != before || tc.body["model"] != nil {
				t.Fatal("changed the shared fixture")
			}
		})
	}
}
