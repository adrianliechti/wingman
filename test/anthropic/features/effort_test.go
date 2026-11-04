package features_test

import (
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestAdaptiveThinkingEffortHTTP(t *testing.T) {
	h := anthropic.New(t)
	h.Client.Timeout = 180 * time.Second

	efforts := []string{"low", "medium", "high", "xhigh", "max"}

	for _, effort := range efforts {
		t.Run(effort, func(t *testing.T) {
			for _, model := range anthropic.DefaultModels() {
				if !model.Capabilities.Thinking {
					continue
				}

				if effort == "xhigh" && !supportsEffortXHigh(model.Name) {
					continue
				}

				t.Run(model.Name, func(t *testing.T) {
					body := map[string]any{
						"max_tokens": 16000,
						"thinking": map[string]any{
							"type":    "adaptive",
							"display": "summarized",
						},
						"output_config": map[string]any{
							"effort": effort,
						},
						"messages": []map[string]any{
							{"role": "user", "content": harness.ReasoningPrompt},
						},
					}

					anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

					if effort == "low" || effort == "medium" {
						requireAdaptiveEffortAnswer(t, "anthropic", anthropicResp.Body)
						requireAdaptiveEffortAnswer(t, "wingman", wingmanResp.Body)
					} else {
						requireThinkingBlock(t, "anthropic", anthropicResp.Body)
						requireThinkingBlock(t, "wingman", wingmanResp.Body)
					}

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
	h.Client.Timeout = 180 * time.Second

	efforts := []string{"low", "medium", "high", "xhigh", "max"}

	for _, effort := range efforts {
		t.Run(effort, func(t *testing.T) {
			for _, model := range anthropic.DefaultModels() {
				if !model.Capabilities.Thinking {
					continue
				}

				if effort == "xhigh" && !supportsEffortXHigh(model.Name) {
					continue
				}

				t.Run(model.Name, func(t *testing.T) {
					body := map[string]any{
						"max_tokens": 16000,
						"thinking": map[string]any{
							"type":    "adaptive",
							"display": "summarized",
						},
						"output_config": map[string]any{
							"effort": effort,
						},
						"messages": []map[string]any{
							{"role": "user", "content": harness.ReasoningPrompt},
						},
					}

					anthropicEvents, wingmanEvents := anthropic.CompareSSE(t, h, model.Name, body)

					if effort == "low" || effort == "medium" {
						requireAdaptiveEffortStream(t, "anthropic", anthropicEvents)
						requireAdaptiveEffortStream(t, "wingman", wingmanEvents)
					} else {
						requireThinkingSSEEvent(t, "anthropic", anthropicEvents)
						requireThinkingSSEEvent(t, "wingman", wingmanEvents)
					}
				})
			}
		})
	}
}

// Low and medium effort permit a model to skip thinking. Validate the actual
// solution in that case, and still validate signed thinking whenever emitted.
func requireAdaptiveEffortAnswer(t *testing.T, label string, body map[string]any) {
	t.Helper()
	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("[%s] content is not an array", label)
	}
	var answer strings.Builder
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] == "thinking" {
			requireThinkingBlock(t, label, body)
		}
		if text, ok := block["text"].(string); ok {
			answer.WriteString(text)
		}
	}
	if !strings.Contains(strings.ReplaceAll(answer.String(), ",", ""), "2425") {
		t.Errorf("[%s] missing correct solution 2425: %q", label, answer.String())
	}
}

func requireAdaptiveEffortStream(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()
	var answer strings.Builder
	thinking, signed := false, false
	for _, event := range events {
		if block, ok := event.Data["content_block"].(map[string]any); ok && block["type"] == "thinking" {
			thinking = true
		}
		if delta, ok := event.Data["delta"].(map[string]any); ok {
			if text, ok := delta["text"].(string); ok {
				answer.WriteString(text)
			}
			if signature, _ := delta["signature"].(string); signature != "" {
				signed = true
			}
		}
	}
	if thinking && !signed {
		t.Errorf("[%s] thinking stream has no signature", label)
	}
	if !strings.Contains(strings.ReplaceAll(answer.String(), ",", ""), "2425") {
		t.Errorf("[%s] missing correct streamed solution 2425: %q", label, answer.String())
	}
}

func TestAdaptiveThinkingNoEffortHTTP(t *testing.T) {
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
					"display": "summarized",
				},
				"messages": []map[string]any{
					{"role": "user", "content": harness.ReasoningPrompt},
				},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			requireThinkingBlock(t, "anthropic", anthropicResp.Body)
			requireThinkingBlock(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content.*.thinking"] = harness.FieldIgnore
			rules["content.*.signature"] = harness.FieldNonEmpty
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
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
				"output_config": map[string]any{"effort": "high"},
				"messages": []map[string]any{
					{"role": "user", "content": harness.ReasoningPrompt},
				},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

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

// supportsEffortXHigh returns true for the Claude families that support
// effort level "xhigh" (Opus 4.7 and later).
func supportsEffortXHigh(name string) bool {
	for _, p := range []string{"opus-4-7", "opus-4-8", "opus-5"} {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// isClaudeAdaptiveModel returns true for the Claude families that support
// adaptive thinking.
func isClaudeAdaptiveModel(name string) bool {
	for _, p := range []string{"sonnet-4-6", "opus-4-6", "opus-4-7", "opus-4-8", "sonnet-5", "opus-5"} {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

func TestThinkingExplicitlyDisabledHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !isClaudeAdaptiveModel(model.Name) {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			body := map[string]any{
				"max_tokens": 1024,
				"thinking": map[string]any{
					"type": "disabled",
				},
				"messages": []map[string]any{
					{"role": "user", "content": "Reply with the single word: OK."},
				},
			}

			resp := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(body, model.Name))
			if resp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			requireNoThinkingBlock(t, resp.Body)
		})
	}
}

func TestThinkingDisabledWithEffortHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !isClaudeAdaptiveModel(model.Name) {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			body := map[string]any{
				"max_tokens": 1024,
				"thinking": map[string]any{
					"type": "disabled",
				},
				"output_config": map[string]any{
					"effort": "low",
				},
				"messages": []map[string]any{
					{"role": "user", "content": "Reply with the single word: OK."},
				},
			}

			resp := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(body, model.Name))
			if resp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			requireNoThinkingBlock(t, resp.Body)
		})
	}
}

func TestEffortWithoutThinkingHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !isClaudeAdaptiveModel(model.Name) {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			body := map[string]any{
				"max_tokens": 1024,
				"output_config": map[string]any{
					"effort": "low",
				},
				"messages": []map[string]any{
					{"role": "user", "content": "Reply with the single word: OK."},
				},
			}

			resp := anthropic.PostMessages(t, h, h.Wingman, anthropic.WithModel(body, model.Name))
			if resp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			requireNoThinkingBlock(t, resp.Body)
		})
	}
}

func requireNoThinkingBlock(t *testing.T, body map[string]any) {
	t.Helper()

	content, ok := body["content"].([]any)
	if !ok {
		t.Fatalf("content is not an array: %v", body["content"])
	}

	for _, block := range content {
		obj, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "thinking" {
			t.Errorf("expected no thinking block, got one")
		}
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
