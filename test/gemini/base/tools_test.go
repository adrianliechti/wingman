package base_test

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/harness"
)

func TestToolCallingHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "What's the weather in London?"}}},
				},
				"tools": []any{gemini.WeatherTool},
			}

			geminiResp, wingmanResp := gemini.CompareHTTP(t, h, model.Name, body)

			requireFunctionCallWithName(t, "gemini", geminiResp.Body, "get_weather")
			requireFunctionCallWithName(t, "wingman", wingmanResp.Body, "get_weather")

			rules := gemini.DefaultResponseRules()
			rules["candidates.*.content.parts.*.functionCall.args"] = harness.FieldPresence
			rules["candidates.*.content.parts.*.functionCall.id"] = harness.FieldIgnore
			harness.CompareStructure(t, "response", geminiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestToolCallingSSE(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "What's the weather in London?"}}},
				},
				"tools": []any{gemini.WeatherTool},
			}

			geminiEvents, wingmanEvents := gemini.CompareSSE(t, h, model.Name, body)

			requireFunctionCallSSE(t, "gemini", geminiEvents, "get_weather")
			requireFunctionCallSSE(t, "wingman", wingmanEvents, "get_weather")
		})
	}
}

// TestToolCallingMultiTurnHTTP exercises a real multi-turn tool-calling
// round trip. Gemini 3 rejects synthetic model turns missing
// thoughtSignature, so we issue turn 1 against each endpoint, replay the
// candidate content verbatim (signatures intact), and only compare the
// final answer.
func TestToolCallingMultiTurnHTTP(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			turn1 := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "What's the weather in London?"}}},
				},
				"tools": []any{gemini.WeatherTool},
			}

			geminiResp1 := gemini.PostGemini(t, h, h.Gemini, h.ReferenceModel, turn1)
			if geminiResp1.StatusCode != 200 {
				t.Fatalf("gemini turn 1 returned %d: %s", geminiResp1.StatusCode, string(geminiResp1.RawBody))
			}
			wingmanResp1 := gemini.PostGemini(t, h, h.Wingman, model.Name, turn1)
			if wingmanResp1.StatusCode != 200 {
				t.Fatalf("wingman turn 1 returned %d: %s", wingmanResp1.StatusCode, string(wingmanResp1.RawBody))
			}

			geminiAssistant, geminiCallID := extractAssistantAndCallID(t, "gemini", geminiResp1.Body, "get_weather")
			wingmanAssistant, wingmanCallID := extractAssistantAndCallID(t, "wingman", wingmanResp1.Body, "get_weather")

			turn2 := func(assistant map[string]any, callID string) map[string]any {
				funcResponse := map[string]any{
					"name":     "get_weather",
					"response": map[string]any{"result": "Sunny, 22°C"},
				}
				if callID != "" {
					funcResponse["id"] = callID
				}

				return map[string]any{
					"contents": []map[string]any{
						{"role": "user", "parts": []map[string]any{{"text": "What's the weather in London?"}}},
						assistant,
						{"role": "user", "parts": []map[string]any{{"functionResponse": funcResponse}}},
					},
					"tools": []any{gemini.WeatherTool},
				}
			}

			geminiResp2 := gemini.PostGemini(t, h, h.Gemini, h.ReferenceModel, turn2(geminiAssistant, geminiCallID))
			if geminiResp2.StatusCode != 200 {
				t.Fatalf("gemini turn 2 returned %d: %s", geminiResp2.StatusCode, string(geminiResp2.RawBody))
			}
			wingmanResp2 := gemini.PostGemini(t, h, h.Wingman, model.Name, turn2(wingmanAssistant, wingmanCallID))
			if wingmanResp2.StatusCode != 200 {
				t.Fatalf("wingman turn 2 returned %d: %s", wingmanResp2.StatusCode, string(wingmanResp2.RawBody))
			}

			gemini.RequireTextResponse(t, "gemini", geminiResp2.Body)
			gemini.RequireTextResponse(t, "wingman", wingmanResp2.Body)

			rules := gemini.DefaultResponseRules()
			harness.CompareStructure(t, "response", geminiResp2.Body, wingmanResp2.Body, harness.CompareOption{Rules: rules})
		})
	}
}

// extractAssistantAndCallID returns the first candidate's assistant content
// (role + parts including any thoughtSignature) and the matching tool-call
// id, so the next turn can replay the model turn verbatim.
func extractAssistantAndCallID(t *testing.T, label string, body map[string]any, name string) (map[string]any, string) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("[%s] no candidates in response", label)
	}

	cand, _ := candidates[0].(map[string]any)
	content, _ := cand["content"].(map[string]any)
	parts, _ := content["parts"].([]any)

	var callID string
	for _, p := range parts {
		part, _ := p.(map[string]any)
		fc, _ := part["functionCall"].(map[string]any)
		if fc == nil {
			continue
		}
		if fc["name"] != name {
			continue
		}
		if id, ok := fc["id"].(string); ok {
			callID = id
		}
		break
	}

	if content == nil {
		t.Fatalf("[%s] candidate has no content", label)
	}

	return map[string]any{
		"role":  "model",
		"parts": parts,
	}, callID
}

func requireFunctionCall(t *testing.T, label string, body map[string]any) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	for _, c := range candidates {
		cand, _ := c.(map[string]any)
		content, _ := cand["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if _, ok := part["functionCall"]; ok {
				return
			}
		}
	}

	t.Fatalf("[%s] no functionCall found in response", label)
}

func requireFunctionCallWithName(t *testing.T, label string, body map[string]any, name string) {
	t.Helper()

	candidates, _ := body["candidates"].([]any)
	for _, c := range candidates {
		cand, _ := c.(map[string]any)
		content, _ := cand["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			fc, _ := part["functionCall"].(map[string]any)
			if fc["name"] == name {
				return
			}
		}
	}

	t.Fatalf("[%s] no functionCall with name %q found", label, name)
}

// requireFunctionCallSSE asserts that some streamed chunk carries a
// functionCall part with the given name. Gemini emits each chunk as a
// full GenerateContentResponse, so we walk candidates[].content.parts[]
// the same way as the HTTP variant.
func requireFunctionCallSSE(t *testing.T, label string, events []*harness.SSEEvent, name string) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}
		candidates, _ := e.Data["candidates"].([]any)
		for _, c := range candidates {
			cand, _ := c.(map[string]any)
			content, _ := cand["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, p := range parts {
				part, _ := p.(map[string]any)
				fc, _ := part["functionCall"].(map[string]any)
				if fc["name"] == name {
					return
				}
			}
		}
	}

	t.Fatalf("[%s] no functionCall SSE event with name %q found", label, name)
}

// TestToolCallingWithTextSSE asks for a sentence before the tool call so the
// stream carries text chunks followed by a functionCall chunk. Clients
// concatenate text parts across chunks, so text that was already streamed
// must not be repeated in the final (functionCall) chunk.
func TestToolCallingWithTextSSE(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]any{{"text": "First write one short sentence telling me what you are about to do, then call get_weather for London."}}},
				},
				"tools": []any{gemini.WeatherTool},
			}

			geminiEvents, wingmanEvents := gemini.CompareSSE(t, h, model.Name, body)

			requireFunctionCallSSE(t, "gemini", geminiEvents, "get_weather")
			requireFunctionCallSSE(t, "wingman", wingmanEvents, "get_weather")

			requireNoRepeatedTextSSE(t, "gemini", geminiEvents)
			requireNoRepeatedTextSSE(t, "wingman", wingmanEvents)
		})
	}
}

// requireNoRepeatedTextSSE fails when a chunk carries text that equals the
// text already streamed before it — the signature of a final chunk that
// re-sends the accumulated message instead of only the new parts.
func requireNoRepeatedTextSSE(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	var streamed string

	for i, e := range events {
		if e.Data == nil {
			continue
		}

		var chunk string

		candidates, _ := e.Data["candidates"].([]any)
		for _, c := range candidates {
			cand, _ := c.(map[string]any)
			content, _ := cand["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, p := range parts {
				part, _ := p.(map[string]any)
				if thought, _ := part["thought"].(bool); thought {
					continue
				}
				if text, ok := part["text"].(string); ok {
					chunk += text
				}
			}
		}

		if chunk != "" && len(chunk) >= 10 && streamed != "" && strings.Contains(streamed, chunk) {
			t.Fatalf("[%s] chunk %d repeats text that was already streamed: %q", label, i, chunk)
		}

		streamed += chunk
	}
}
