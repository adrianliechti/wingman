package messages_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestAdaptiveThinkingEffortHTTP(t *testing.T) {
	h := anthropic.New(t)

	efforts := []string{"low", "medium", "high", "max"}

	for _, effort := range efforts {
		t.Run(effort, func(t *testing.T) {
			for _, model := range anthropic.DefaultModels() {
				if !model.Capabilities.Thinking {
					continue
				}

				t.Run(model.Name, func(t *testing.T) {
					body := map[string]any{
						"max_tokens": 16000,
						"thinking": map[string]any{
							"type": "adaptive",
						},
						"output_config": map[string]any{
							"effort": effort,
						},
						"messages": []map[string]any{
							{"role": "user", "content": "How many r's are in strawberry?"},
						},
					}

					anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

					requireThinkingBlock(t, "anthropic", anthropicResp.Body)
					requireThinkingBlock(t, "wingman", wingmanResp.Body)

					rules := anthropic.DefaultMessagesResponseRules()
					rules["content.*.thinking"] = harness.FieldIgnore
					rules["content.*.signature"] = harness.FieldNonEmpty
					harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
				})
			}
		})
	}
}

func TestAdaptiveThinkingEffortSSE(t *testing.T) {
	h := anthropic.New(t)

	efforts := []string{"low", "medium", "high", "max"}

	for _, effort := range efforts {
		t.Run(effort, func(t *testing.T) {
			for _, model := range anthropic.DefaultModels() {
				if !model.Capabilities.Thinking {
					continue
				}

				t.Run(model.Name, func(t *testing.T) {
					body := map[string]any{
						"max_tokens": 16000,
						"thinking": map[string]any{
							"type": "adaptive",
						},
						"output_config": map[string]any{
							"effort": effort,
						},
						"messages": []map[string]any{
							{"role": "user", "content": "How many r's are in strawberry?"},
						},
					}

					anthropicEvents, wingmanEvents := compareSSE(t, h, model.Name, body)

					requireThinkingSSEEvent(t, "anthropic", anthropicEvents)
					requireThinkingSSEEvent(t, "wingman", wingmanEvents)
				})
			}
		})
	}
}

func TestAdaptiveThinkingDisplayOmittedHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 16000,
				"thinking": map[string]any{
					"type":    "adaptive",
					"display": "omitted",
				},
				"messages": []map[string]any{
					{"role": "user", "content": "How many r's are in strawberry?"},
				},
			}

			anthropicResp, wingmanResp := compareHTTP(t, h, model.Name, body)

			// With display=omitted, thinking blocks should still have a signature
			// but the thinking text should be empty
			requireThinkingBlockOmitted(t, "anthropic", anthropicResp.Body)
			requireThinkingBlockOmitted(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content.*.thinking"] = harness.FieldIgnore
			rules["content.*.signature"] = harness.FieldNonEmpty
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// isClaudeAdaptiveModel returns true for the Claude families that support
// adaptive thinking — the default-adaptive behavior is Claude-specific.
func isClaudeAdaptiveModel(name string) bool {
	for _, p := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-opus-4-7", "bedrock-sonnet-4-6"} {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// TestAdaptiveThinkingDefaultHTTP verifies that adaptive-thinking Claude
// models produce a thinking block by default — when the caller does not
// pass any `thinking` or `output_config` fields. Wingman-only: the upstream
// Anthropic API does not enable thinking by default, so we don't compare
// structurally.
func TestAdaptiveThinkingDefaultHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !isClaudeAdaptiveModel(model.Name) {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 16000,
				"messages": []map[string]any{
					{"role": "user", "content": "How many r's are in strawberry?"},
				},
			}

			resp := postAnthropic(t, h, h.Wingman, withModel(body, model.Name))
			if resp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			requireThinkingBlock(t, "wingman", resp.Body)
		})
	}
}

// TestAdaptiveThinkingDefaultSSE mirrors TestAdaptiveThinkingDefaultHTTP for
// the streaming surface — when no thinking config is sent, wingman should
// still emit a thinking content_block_start event for adaptive-thinking
// Claude models.
func TestAdaptiveThinkingDefaultSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !isClaudeAdaptiveModel(model.Name) {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 16000,
				"stream":     true,
				"messages": []map[string]any{
					{"role": "user", "content": "How many r's are in strawberry?"},
				},
			}

			events := postAnthropicSSE(t, h, h.Wingman, withModel(body, model.Name))
			if len(events) == 0 {
				t.Fatal("wingman returned no SSE events")
			}

			requireThinkingSSEEvent(t, "wingman", events)
		})
	}
}

// TestThinkingExplicitlyDisabledHTTP verifies that the explicit
// `thinking: {type: "disabled"}` request still suppresses thinking on
// adaptive thinking Claude models, even after the default-adaptive change.
func TestThinkingExplicitlyDisabledHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !isClaudeAdaptiveModel(model.Name) {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"thinking": map[string]any{
					"type": "disabled",
				},
				"messages": []map[string]any{
					{"role": "user", "content": "Reply with the single word: OK."},
				},
			}

			resp := postAnthropic(t, h, h.Wingman, withModel(body, model.Name))
			if resp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			content, ok := resp.Body["content"].([]any)
			if !ok {
				t.Fatalf("content is not an array: %v", resp.Body["content"])
			}

			for _, block := range content {
				obj, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if obj["type"] == "thinking" {
					t.Errorf("expected no thinking block with thinking.type=disabled, got one")
				}
			}
		})
	}
}

func requireThinkingBlockOmitted(t *testing.T, label string, body map[string]any) {
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

		if obj["type"] == "thinking" {
			thinking, _ := obj["thinking"].(string)
			if thinking != "" {
				t.Errorf("[%s] thinking block should be empty with display=omitted, got %q", label, thinking[:min(50, len(thinking))])
			}

			signature, _ := obj["signature"].(string)
			if signature == "" {
				t.Errorf("[%s] thinking block has empty signature", label)
			}

			return
		}
	}

	t.Fatalf("[%s] no thinking content block found", label)
}
