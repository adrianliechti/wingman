package features_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// namespaceWeatherTool is a single function tool grouped under a Codex-style
// MCP namespace ("mcp__weather__"). Both OpenAI and wingman must accept the
// shape and surface tool calls with the namespace field intact.
var namespaceWeatherTool = map[string]any{
	"type":        "namespace",
	"name":        "mcp__weather__",
	"description": "Tools for working with the weather MCP server.",
	"tools": []any{
		map[string]any{
			"type":        "function",
			"name":        "get_weather",
			"description": "Get the current weather for a location.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "The city and country, e.g. 'London, UK'.",
					},
				},
				"required": []string{"location"},
			},
		},
	},
}

func TestNamespaceToolHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "What's the weather in London?",
				"tools": []any{namespaceWeatherTool},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			requireNamespacedFunctionCall(t, "openai", openaiResp.Body, "get_weather", "mcp__weather__")
			requireNamespacedFunctionCall(t, "wingman", wingmanResp.Body, "get_weather", "mcp__weather__")

			rules := openai.DefaultResponsesResponseRules()
			rules["output"] = harness.FieldPresence
			rules["tools"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestNamespaceToolSSE(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			// The SSE comparison is strict on the event-type pattern; without
			// this nudge some models write preamble text before the call,
			// which the reference model does not.
			body := map[string]any{
				"input": "What's the weather in London? Call the tool directly, without any introduction or commentary.",
				"tools": []any{namespaceWeatherTool},
			}

			openaiEvents, wingmanEvents := responses.CompareSSE(t, h, model, body)

			requireNamespacedFunctionCallSSE(t, "openai", openaiEvents, "get_weather", "mcp__weather__")
			requireNamespacedFunctionCallSSE(t, "wingman", wingmanEvents, "get_weather", "mcp__weather__")
		})
	}
}

func TestNamespaceToolRoundTripHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			turn1 := map[string]any{
				"input": "What's the weather in London? Use the tool.",
				"tools": []any{namespaceWeatherTool},
			}

			_, wingmanResp1 := responses.CompareHTTP(t, h, model, turn1)

			call := findNamespacedFunctionCall(t, "wingman", wingmanResp1.Body, "get_weather", "mcp__weather__")

			turn2 := map[string]any{
				"input": []any{
					map[string]any{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "What's the weather in London? Use the tool."},
						},
					},
					call,
					map[string]any{
						"type":    "function_call_output",
						"call_id": call["call_id"],
						"output":  "Sunny, 22°C with light winds",
					},
				},
				"tools": []any{namespaceWeatherTool},
			}

			h.RequireConfigured(t, model.Name)

			wingmanResp2, err := h.Client.Post(context.Background(), h.Wingman, "/responses", responses.WithModel(turn2, model.Name))
			if err != nil {
				t.Fatalf("wingman turn 2 failed: %v", err)
			}
			if wingmanResp2.StatusCode != 200 {
				t.Fatalf("wingman turn 2 returned %d: %s", wingmanResp2.StatusCode, string(wingmanResp2.RawBody))
			}

			responses.RequireMessageOutput(t, "wingman", wingmanResp2.Body)
		})
	}
}

func findNamespacedFunctionCall(t *testing.T, label string, body map[string]any, name, namespace string) map[string]any {
	t.Helper()

	output, _ := body["output"].([]any)

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "function_call" && obj["name"] == name && obj["namespace"] == namespace {
			return obj
		}
	}

	t.Fatalf("[%s] no namespaced function_call %q found in output: %+v", label, name, output)
	return nil
}

func requireNamespacedFunctionCall(t *testing.T, label string, body map[string]any, name, namespace string) {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array: %+v", label, body["output"])
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] != "function_call" || obj["name"] != name {
			continue
		}
		if obj["namespace"] != namespace {
			t.Fatalf("[%s] function_call %q missing namespace %q: %+v", label, name, namespace, obj)
		}
		return
	}

	t.Fatalf("[%s] no namespaced function_call %q found in output: %+v", label, name, output)
}

func requireNamespacedFunctionCallSSE(t *testing.T, label string, events []*harness.SSEEvent, name, namespace string) {
	t.Helper()

	for _, ev := range events {
		if ev.Event != "response.output_item.done" {
			continue
		}
		item, ok := ev.Data["item"].(map[string]any)
		if !ok {
			continue
		}
		if item["type"] != "function_call" || item["name"] != name {
			continue
		}
		if item["namespace"] != namespace {
			t.Fatalf("[%s] streamed function_call %q missing namespace %q: %+v", label, name, namespace, item)
		}
		return
	}

	t.Fatalf("[%s] no streamed namespaced function_call %q found", label, name)
}
