package features_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// toolSearchClient is the client-executed flavor: the model asks for tools,
// the client returns them via tool_search_output on a follow-up turn.
var toolSearchClient = map[string]any{
	"type":        "tool_search",
	"execution":   "client",
	"description": "Find project-specific tools needed to satisfy the user's request.",
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal": map[string]any{
				"type":        "string",
				"description": "What the user is trying to accomplish.",
			},
		},
		"required": []string{"goal"},
	},
}

// deferredWeatherTool is a function tool hidden from the initial list. The
// model can only call it after surfacing it through tool_search.
var deferredWeatherTool = map[string]any{
	"type":          "function",
	"name":          "get_weather",
	"description":   "Get the current weather for a location.",
	"defer_loading": true,
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The city and country.",
			},
		},
		"required": []string{"location"},
	},
}

// TestToolSearchHTTP forwards a `tool_search` tool plus a `defer_loading: true`
// function and compares wingman's response shape to OpenAI's. The model may
// emit either a tool_search_call (when it decides to search) or a
// function_call (if it ignored the tool_search flow) — both endpoints must
// agree on the shape.
func TestToolSearchHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "What's the weather in London? Use any tools you have available to find out.",
				"tools": []any{toolSearchClient, deferredWeatherTool},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			rules := openai.DefaultResponsesResponseRules()
			rules["output"] = harness.FieldPresence
			rules["output.*.arguments"] = harness.FieldIgnore
			rules["tools"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// TestToolSearchHostedHTTP exercises the hosted (server-executed) flavor:
// the API searches the deferred tools itself, and the model must end up
// calling the discovered get_weather function within the same turn.
func TestToolSearchHostedHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.ToolSearch {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "What's the weather in London? Use any tools you have available to find out.",
				"tools": []any{
					map[string]any{"type": "tool_search"},
					deferredWeatherTool,
				},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			requireFunctionCall(t, "openai", openaiResp.Body, "get_weather")
			requireFunctionCall(t, "wingman", wingmanResp.Body, "get_weather")

			rules := openai.DefaultResponsesResponseRules()
			rules["output"] = harness.FieldPresence
			rules["tools"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func requireFunctionCall(t *testing.T, label string, body map[string]any, name string) {
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

		if obj["type"] == "function_call" && obj["name"] == name {
			return
		}
	}

	t.Fatalf("[%s] no function_call named %q found", label, name)
}

// TestToolSearchSSE is the streaming variant; the SSE event sequence and item
// shape must agree between wingman and OpenAI for the same request.
func TestToolSearchSSE(t *testing.T) {
	t.Skip("tool_search streaming semantics are not implemented consistently across backing providers yet")

	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "What's the weather in London? Use any tools you have available to find out.",
				"tools": []any{toolSearchClient, deferredWeatherTool},
			}

			_, _ = responses.CompareSSE(t, h, model, body)
		})
	}
}

// TestToolSearchOutputRoundTrip exercises the multi-turn case: the prior turn
// is a tool_search_call/output pair, and the new turn asks the model to use
// the surfaced tool. Both endpoints must accept the round-trip input shape
// without error.
func TestToolSearchOutputRoundTripHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"tools": []any{toolSearchClient, deferredWeatherTool},
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "What's the weather in London?"},
						},
					},
					{
						"type":      "tool_search_call",
						"id":        "tsc_prior",
						"call_id":   "call_prior_ts",
						"status":    "completed",
						"execution": "client",
						"arguments": map[string]any{"goal": "find a weather tool"},
					},
					{
						"type":      "tool_search_output",
						"call_id":   "call_prior_ts",
						"status":    "completed",
						"execution": "client",
						"tools":     []any{deferredWeatherTool},
					},
				},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			rules := openai.DefaultResponsesResponseRules()
			rules["output"] = harness.FieldPresence
			rules["output.*.arguments"] = harness.FieldIgnore
			rules["tools"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}
