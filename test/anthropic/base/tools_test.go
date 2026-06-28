package base_test

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestToolCallingHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{anthropic.WeatherTool},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			requireToolUseWithName(t, "anthropic", anthropicResp.Body, "get_weather")
			requireToolUseWithName(t, "wingman", wingmanResp.Body, "get_weather")

			rules := anthropic.DefaultMessagesResponseRules()
			rules["content"] = harness.FieldPresence
			rules["content.*.id"] = harness.FieldPresence
			rules["content.*.input"] = harness.FieldPresence
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolCallingSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			anthropicBody := anthropic.WithModel(map[string]any{
				"max_tokens": 1024,
				"stream":     true,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{anthropic.WeatherTool},
			}, h.ReferenceModel)

			wingmanBody := anthropic.WithModel(map[string]any{
				"max_tokens": 1024,
				"stream":     true,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
				},
				"tools": []any{anthropic.WeatherTool},
			}, model.Name)

			anthropicEvents := anthropic.PostMessagesSSE(t, h, h.Anthropic, anthropicBody)
			wingmanEvents := anthropic.PostMessagesSSE(t, h, h.Wingman, wingmanBody)

			requireToolUseSSE(t, "anthropic", anthropicEvents)
			requireToolUseSSE(t, "wingman", wingmanEvents)
		})
	}
}

func TestToolCallingMultiTurnHTTP(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"max_tokens": 1024,
				"messages": []map[string]any{
					{"role": "user", "content": "What's the weather in London?"},
					{
						"role": "assistant",
						"content": []map[string]any{
							{
								"type":  "tool_use",
								"id":    "toolu_test123",
								"name":  "get_weather",
								"input": map[string]any{"location": "London, UK"},
							},
						},
					},
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type":        "tool_result",
								"tool_use_id": "toolu_test123",
								"content":     "Sunny, 22°C with light winds",
							},
						},
					},
				},
				"tools": []any{anthropic.WeatherTool},
			}

			anthropicResp, wingmanResp := anthropic.CompareHTTP(t, h, model.Name, body)

			anthropic.RequireTextContent(t, "anthropic", anthropicResp.Body)
			anthropic.RequireTextContent(t, "wingman", wingmanResp.Body)

			rules := anthropic.DefaultMessagesResponseRules()
			harness.CompareStructure(t, "response", anthropicResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// longNoteTool has a single free-text argument. Forcing the model to fill it
// with a large value makes the tool_use input span many input_json_delta
// events, which exercises (and guards against) truncated-chunk reassembly.
var longNoteTool = map[string]any{
	"name":        "save_note",
	"description": "Persist a note containing arbitrary text",
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The full, verbatim text of the note",
			},
		},
		"required": []string{"content"},
	},
}

// Varied original prose (not repetition, which can trip output content filters)
// reliably yields a long argument that spans many streamed deltas.
const longNotePrompt = "Call save_note exactly once. Set its content argument to a vivid, original " +
	"description of an imaginary coastal city — at least 200 words of flowing prose. " +
	"Put the full description directly in the content value."

func TestToolCallingLongInputSSE(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			req := map[string]any{
				"max_tokens": 4096,
				"stream":     true,
				"messages": []map[string]any{
					{"role": "user", "content": longNotePrompt},
				},
				"tools":       []any{longNoteTool},
				"tool_choice": map[string]any{"type": "tool", "name": "save_note"},
			}

			anthropicEvents := anthropic.PostMessagesSSE(t, h, h.Anthropic, anthropic.WithModel(req, h.ReferenceModel))
			wingmanEvents := anthropic.PostMessagesSSE(t, h, h.Wingman, anthropic.WithModel(req, model.Name))

			requireLongToolInputSSE(t, "anthropic", anthropicEvents)
			requireLongToolInputSSE(t, "wingman", wingmanEvents)
		})
	}
}

// requireLongToolInputSSE concatenates input_json_delta.partial_json fragments
// per tool_use block and asserts the result is complete, valid JSON with a long
// value. A dropped or partial chunk fails the json.Unmarshal here.
func requireLongToolInputSSE(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	inputs := map[int]string{}
	toolBlock := map[int]bool{}
	var order []int

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		idx := 0
		if f, ok := e.Data["index"].(float64); ok {
			idx = int(f)
		}

		switch e.Data["type"] {
		case "content_block_start":
			block, _ := e.Data["content_block"].(map[string]any)
			if block != nil && block["type"] == "tool_use" {
				if !toolBlock[idx] {
					order = append(order, idx)
				}
				toolBlock[idx] = true
			}

		case "content_block_delta":
			delta, _ := e.Data["delta"].(map[string]any)
			if delta != nil && delta["type"] == "input_json_delta" {
				pj, _ := delta["partial_json"].(string)
				inputs[idx] += pj
			}
		}
	}

	if len(order) == 0 {
		t.Fatalf("[%s] no tool_use block found in stream", label)
	}

	args := inputs[order[0]]

	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Fatalf("[%s] reassembled tool_use input is not valid JSON (likely a truncated/dropped chunk): %v\n  length=%d\n  tail=%q", label, err, len(args), tail(args))
	}

	content, ok := parsed["content"].(string)
	if !ok {
		t.Fatalf("[%s] reassembled input missing string 'content' field: %s", label, args)
	}

	if len(content) < 400 {
		t.Fatalf("[%s] expected a long 'content' spanning multiple input_json_delta events, got %d chars", label, len(content))
	}
}

func tail(s string) string {
	const n = 80
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func requireToolUseWithName(t *testing.T, label string, body map[string]any, name string) {
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
		if obj["type"] == "tool_use" && obj["name"] == name {
			return
		}
	}

	t.Fatalf("[%s] no tool_use block with name %q found", label, name)
}

func requireToolUseSSE(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		eventType, _ := e.Data["type"].(string)
		if eventType != "content_block_start" {
			continue
		}

		block, ok := e.Data["content_block"].(map[string]any)
		if !ok {
			continue
		}

		if block["type"] == "tool_use" {
			return
		}
	}

	t.Fatalf("[%s] no tool_use SSE event found", label)
}
