package base_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

var weatherTool = map[string]any{
	"type":        "function",
	"name":        "get_weather",
	"description": "Get the current weather for a location",
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The city and country, e.g. 'London, UK'",
			},
		},
		"required": []string{"location"},
	},
}

func TestToolCallingHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "What's the weather in London?",
				"tools": []any{weatherTool},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			// Both should return function_call output items
			requireFunctionCall(t, "openai", openaiResp.Body, "get_weather")
			requireFunctionCall(t, "wingman", wingmanResp.Body, "get_weather")

			rules := openai.DefaultResponsesResponseRules()
			// Some models return extra output items (e.g. message + function_call)
			rules["output"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolCallingSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)
			openaiBody := responses.WithModel(map[string]any{
				"input":  "What's the weather in London?",
				"tools":  []any{weatherTool},
				"stream": true,
			}, h.ReferenceModel)

			wingmanBody := responses.WithModel(map[string]any{
				"input":  "What's the weather in London?",
				"tools":  []any{weatherTool},
				"stream": true,
			}, model.Name)

			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", openaiBody)
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", wingmanBody)
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			requireFunctionCallSSEEvent(t, "openai", openaiEvents, "get_weather")
			requireFunctionCallSSEEvent(t, "wingman", wingmanEvents, "get_weather")
		})
	}
}

func TestToolCallingMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			// Simulate a full tool calling round-trip:
			// 1. User asks about weather
			// 2. Assistant calls get_weather
			// 3. Tool returns result
			// 4. Assistant responds with final answer
			body := map[string]any{
				"tools": []any{weatherTool},
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "What's the weather in London?"},
						},
					},
					{
						"type":      "function_call",
						"id":        "fc_test123",
						"call_id":   "call_test123",
						"name":      "get_weather",
						"arguments": `{"location": "London, UK"}`,
					},
					{
						"type":    "function_call_output",
						"call_id": "call_test123",
						"output":  "Sunny, 22°C with light winds",
					},
				},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			// Final response should be a message (not another tool call)
			responses.RequireMessageOutput(t, "openai", openaiResp.Body)
			responses.RequireMessageOutput(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolChoiceNoneHTTP(t *testing.T) {
	h := openai.New(t)

	tools := []any{weatherTool}

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input":       "What is 2+2? Answer directly.",
				"tools":       tools,
				"tool_choice": "none",
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			responses.RequireMessageOutput(t, "openai", openaiResp.Body)
			responses.RequireMessageOutput(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// longNoteTool has a single free-text argument. Forcing the model to fill it
// with a large value makes the tool-call arguments span many streamed deltas,
// which exercises (and guards against) truncated/dropped-chunk reassembly.
var longNoteTool = map[string]any{
	"type":        "function",
	"name":        "save_note",
	"description": "Persist a note containing arbitrary text",
	"parameters": map[string]any{
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

func TestToolCallingLongArgumentsSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			req := map[string]any{
				"stream": true,
				"input":  longNotePrompt,
				"tools":  []any{longNoteTool},
				"tool_choice": map[string]any{
					"type": "function",
					"name": "save_note",
				},
			}

			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", responses.WithModel(req, h.ReferenceModel))
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", responses.WithModel(req, model.Name))
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			requireLongToolArgumentsResponses(t, "openai", openaiEvents)
			requireLongToolArgumentsResponses(t, "wingman", wingmanEvents)
		})
	}
}

// requireLongToolArgumentsResponses concatenates the streamed
// function_call_arguments.delta fragments (keyed by item_id) and asserts the
// result is complete, valid JSON. It also checks the terminal
// function_call_arguments.done carries the same string — a cut-off tool call
// that still ships a "done" item with truncated JSON fails here.
func requireLongToolArgumentsResponses(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	deltas := map[string]string{}
	done := map[string]string{}
	var order []string

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		switch e.Data["type"] {
		case "response.function_call_arguments.delta":
			id, _ := e.Data["item_id"].(string)
			if _, seen := deltas[id]; !seen {
				order = append(order, id)
			}
			d, _ := e.Data["delta"].(string)
			deltas[id] += d

		case "response.function_call_arguments.done":
			id, _ := e.Data["item_id"].(string)
			a, _ := e.Data["arguments"].(string)
			done[id] = a
		}
	}

	if len(order) == 0 {
		t.Fatalf("[%s] no function_call_arguments.delta events found in stream", label)
	}

	id := order[0]

	if d, ok := done[id]; ok && d != deltas[id] {
		t.Fatalf("[%s] function_call_arguments.done does not match reassembled deltas (truncation):\n  done len=%d tail=%q\n  deltas len=%d tail=%q", label, len(d), argsTail(d), len(deltas[id]), argsTail(deltas[id]))
	}

	requireLongJSONArguments(t, label, deltas[id])
}

// requireLongJSONArguments asserts that a reassembled tool-call argument string
// is complete, valid JSON carrying a long "content" value. A truncated stream
// (a dropped or partial chunk) fails the json.Unmarshal here.
func requireLongJSONArguments(t *testing.T, label, args string) {
	t.Helper()

	if args == "" {
		t.Fatalf("[%s] reassembled tool arguments are empty", label)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Fatalf("[%s] reassembled tool arguments are not valid JSON (likely a truncated/dropped stream chunk): %v\n  length=%d\n  tail=%q", label, err, len(args), argsTail(args))
	}

	content, ok := parsed["content"].(string)
	if !ok {
		t.Fatalf("[%s] reassembled arguments missing string 'content' field: %s", label, args)
	}

	if len(content) < 400 {
		t.Fatalf("[%s] expected a long 'content' spanning multiple stream chunks, got %d chars", label, len(content))
	}
}

func argsTail(s string) string {
	const n = 80
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// requireFunctionCall checks that the response contains a function_call with the given name.
func requireFunctionCall(t *testing.T, label string, body map[string]any, name string) {
	t.Helper()

	if hasFunctionCall(body, name) {
		return
	}

	t.Fatalf("[%s] no function_call output item with name %q found", label, name)
}

func hasFunctionCall(body map[string]any, name string) bool {
	output, ok := body["output"].([]any)
	if !ok {
		return false
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "function_call" && obj["name"] == name {
			args, _ := obj["arguments"].(string)
			if args != "" {
				var parsed map[string]any
				if json.Unmarshal([]byte(args), &parsed) == nil {
					return true
				}
			}
		}
	}

	return false
}

// requireAnyFunctionCall checks that the response contains at least one function_call.
func requireAnyFunctionCall(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] == "function_call" {
			return
		}
	}

	t.Fatalf("[%s] no function_call output item found", label)
}

// requireFunctionCallSSEEvent checks that the SSE stream contains a function_call output_item.
func requireFunctionCallSSEEvent(t *testing.T, label string, events []*harness.SSEEvent, name string) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		itemType, _ := e.Data["type"].(string)
		if itemType != "response.output_item.added" {
			continue
		}

		item, ok := e.Data["item"].(map[string]any)
		if !ok {
			continue
		}

		if item["type"] == "function_call" && item["name"] == name {
			return
		}
	}

	t.Fatalf("[%s] no function_call SSE event with name %q found", label, name)
}
