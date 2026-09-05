// Package hard_test holds the demanding GenerateContent scenarios. Every
// scenario runs against Google with the reference model and against Wingman
// with each configured backend.
package hard_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/adrianliechti/wingman/test/gemini"
	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/harness"
)

var clockTool = map[string]any{
	"functionDeclarations": []map[string]any{{
		"name":        "get_time",
		"description": "Get the current time",
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
	}},
}

type target struct {
	label string
	ep    harness.Endpoint
	model string
}

func targets(h *gemini.Harness, model gemini.Model) []target {
	return []target{
		{"gemini", h.Gemini, h.ReferenceModel},
		{"wingman", h.Wingman, model.Name},
	}
}

func post(t *testing.T, h *gemini.Harness, tg target, body map[string]any) map[string]any {
	t.Helper()

	resp := gemini.PostGemini(t, h, tg.ep, tg.model, body)
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", tg.label, resp.StatusCode, string(resp.RawBody))
	}

	return resp.Body
}

func postSSE(t *testing.T, h *gemini.Harness, tg target, body map[string]any) []*harness.SSEEvent {
	t.Helper()

	events := gemini.PostGeminiSSE(t, h, tg.ep, tg.model, body)
	if len(events) == 0 {
		t.Fatalf("[%s] no SSE events", tg.label)
	}

	return events
}

func user(text string) []any {
	return []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}}}
}

func candidate(body map[string]any) map[string]any {
	candidates, _ := body["candidates"].([]any)
	if len(candidates) == 0 {
		return map[string]any{}
	}
	c, _ := candidates[0].(map[string]any)
	return c
}

func parts(body map[string]any) []map[string]any {
	content, _ := candidate(body)["content"].(map[string]any)
	raw, _ := content["parts"].([]any)

	var out []map[string]any
	for _, p := range raw {
		if m, ok := p.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func messageText(body map[string]any) string {
	var b strings.Builder
	for _, p := range parts(body) {
		if thought, _ := p["thought"].(bool); thought {
			continue
		}
		text, _ := p["text"].(string)
		b.WriteString(text)
	}
	return b.String()
}

func functionCalls(body map[string]any) []map[string]any {
	var calls []map[string]any
	for _, p := range parts(body) {
		if fc, ok := p["functionCall"].(map[string]any); ok {
			calls = append(calls, fc)
		}
	}
	return calls
}

// toolLoop replays the model content verbatim (thought signatures included)
// and answers every functionCall until the model answers with text.
func toolLoop(t *testing.T, h *gemini.Harness, tg target, body map[string]any) (int, string) {
	t.Helper()

	contents := append([]any{}, body["contents"].([]any)...)
	calls := 0

	for round := 0; round < 6; round++ {
		req := gemini.WithModel(body, tg.model)
		req["contents"] = contents

		resp := post(t, h, tg, req)

		fcs := functionCalls(resp)
		if len(fcs) == 0 {
			return calls, messageText(resp)
		}

		content, _ := candidate(resp)["content"].(map[string]any)
		contents = append(contents, map[string]any{"role": "model", "parts": content["parts"]})

		var responses []any
		for _, fc := range fcs {
			calls++

			name, _ := fc["name"].(string)
			args, _ := json.Marshal(fc["args"])

			response := map[string]any{
				"name":     name,
				"response": map[string]any{"result": hardtest.ToolResult(name, string(args))},
			}
			if id, _ := fc["id"].(string); id != "" {
				response["id"] = id
			}

			responses = append(responses, map[string]any{"functionResponse": response})
		}

		contents = append(contents, map[string]any{"role": "user", "parts": responses})
	}

	t.Fatalf("[%s] tool loop did not finish within 6 rounds", tg.label)
	return 0, ""
}

func TestParallelToolCalls(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":    []any{gemini.WeatherTool},
				"contents": user(hardtest.ParallelPrompt),
			}

			for _, tg := range targets(h, model) {
				calls, answer := toolLoop(t, h, tg, body)

				if calls < 3 {
					t.Errorf("[%s] expected one call per city, got %d", tg.label, calls)
				}
				hardtest.RequireMentionsCities(t, tg.label, answer)
			}
		})
	}
}

func TestToolLoopWithThinking(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":            []any{gemini.WeatherTool, clockTool},
				"generationConfig": map[string]any{"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingLevel": "low"}},
				"contents":         user(hardtest.ChainedPrompt),
			}

			for _, tg := range targets(h, model) {
				calls, answer := toolLoop(t, h, tg, body)

				if calls < 2 {
					t.Errorf("[%s] expected the time and the weather call, got %d", tg.label, calls)
				}
				if answer == "" {
					t.Errorf("[%s] no final answer", tg.label)
				}
			}
		})
	}
}

func TestToolChoice(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			for _, tg := range targets(h, model) {
				forced := post(t, h, tg, map[string]any{
					"tools":      []any{gemini.WeatherTool},
					"toolConfig": map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}},
					"contents":   user("Say hello."),
				})
				if len(functionCalls(forced)) == 0 {
					t.Errorf("[%s] mode ANY produced no functionCall: %q", tg.label, messageText(forced))
				}

				none := post(t, h, tg, map[string]any{
					"tools":      []any{gemini.WeatherTool},
					"toolConfig": map[string]any{"functionCallingConfig": map[string]any{"mode": "NONE"}},
					"contents":   user("What's the weather in London?"),
				})
				// Claude answers with empty content when the only useful move
				// is the suppressed tool call, so only the absence of a call is
				// asserted.
				if len(functionCalls(none)) != 0 {
					t.Errorf("[%s] mode NONE still produced a functionCall", tg.label)
				}
			}
		})
	}
}

func TestArgumentlessToolStreaming(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":    []any{clockTool},
				"contents": user("What time is it? Use the tool."),
			}

			for _, tg := range targets(h, model) {
				calls := streamedFunctionCalls(postSSE(t, h, tg, body))
				if len(calls) == 0 {
					t.Fatalf("[%s] no functionCall streamed", tg.label)
				}
				for _, fc := range calls {
					if fc["name"] != "get_time" {
						t.Errorf("[%s] unexpected call %v", tg.label, fc)
					}
					if _, ok := fc["args"].(map[string]any); fc["args"] != nil && !ok {
						t.Errorf("[%s] args is not an object: %v", tg.label, fc["args"])
					}
				}
			}
		})
	}
}

func TestStreamingReassembly(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":    []any{gemini.WeatherTool},
				"contents": user(hardtest.TextThenToolPrompt),
			}

			for _, tg := range targets(h, model) {
				events := postSSE(t, h, tg, body)

				calls := streamedFunctionCalls(events)
				if len(calls) == 0 {
					t.Fatalf("[%s] no functionCall streamed", tg.label)
				}
				for _, fc := range calls {
					args, _ := fc["args"].(map[string]any)
					if args["location"] == nil {
						t.Errorf("[%s] functionCall without location: %v", tg.label, fc)
					}
				}

				requireNoRepeatedText(t, tg.label, events)

				if fr := finishReason(events); fr != "STOP" {
					t.Errorf("[%s] finishReason %q, expected STOP", tg.label, fr)
				}
			}
		})
	}
}

func TestUnicodeStreaming(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{"contents": user(hardtest.UnicodePrompt)}

			for _, tg := range targets(h, model) {
				hardtest.RequireUnicode(t, tg.label, streamedText(postSSE(t, h, tg, body)))
			}
		})
	}
}

func TestTruncation(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"generationConfig": map[string]any{"maxOutputTokens": 32},
				"contents":         user(hardtest.EssayPrompt),
			}

			for _, tg := range targets(h, model) {
				if fr := candidate(post(t, h, tg, body))["finishReason"]; fr != "MAX_TOKENS" {
					t.Errorf("[%s] finishReason %v, expected MAX_TOKENS", tg.label, fr)
				}
				if fr := finishReason(postSSE(t, h, tg, body)); fr != "MAX_TOKENS" {
					t.Errorf("[%s] streamed finishReason %q, expected MAX_TOKENS", tg.label, fr)
				}
			}
		})
	}
}

func TestStopSequence(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"generationConfig": map[string]any{"stopSequences": []string{"6"}},
				"contents":         user(hardtest.CountingPrompt),
			}

			for _, tg := range targets(h, model) {
				hardtest.RequireStoppedBeforeSix(t, tg.label, messageText(post(t, h, tg, body)))
			}
		})
	}
}

func TestImageInput(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"contents": []any{map[string]any{
					"role": "user",
					"parts": []any{
						map[string]any{"inlineData": map[string]any{
							"mimeType": "image/png",
							"data":     base64.StdEncoding.EncodeToString(hardtest.RedSquarePNG()),
						}},
						map[string]any{"text": hardtest.ColorPrompt},
					},
				}},
			}

			for _, tg := range targets(h, model) {
				hardtest.RequireRed(t, tg.label, messageText(post(t, h, tg, body)))
			}
		})
	}
}

func TestStructuredOutputStreaming(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		if !model.Capabilities.StructuredOutput {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"contents": user(hardtest.BookPrompt),
				"generationConfig": map[string]any{
					"responseMimeType":   "application/json",
					"responseJsonSchema": hardtest.BookSchema,
				},
			}

			for _, tg := range targets(h, model) {
				hardtest.RequireBookJSON(t, tg.label, streamedText(postSSE(t, h, tg, body)))
			}
		})
	}
}

func TestLongContextNeedle(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{"contents": user(hardtest.NeedlePrompt())}

			for _, tg := range targets(h, model) {
				hardtest.RequireNeedle(t, tg.label, messageText(post(t, h, tg, body)))
			}
		})
	}
}

func TestErrorToolResult(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			// Gemini 3 needs a signature on replayed calls; run the first turn
			// live and answer it with an error payload.
			body := map[string]any{
				"tools":    []any{gemini.WeatherTool},
				"contents": user("What's the weather in London? Use the tool."),
			}

			for _, tg := range targets(h, model) {
				first := post(t, h, tg, body)
				fcs := functionCalls(first)
				if len(fcs) == 0 {
					t.Fatalf("[%s] turn 1 produced no functionCall", tg.label)
				}

				content, _ := candidate(first)["content"].(map[string]any)
				contents := append([]any{}, body["contents"].([]any)...)
				contents = append(contents, map[string]any{"role": "model", "parts": content["parts"]})

				var responses []any
				for _, fc := range fcs {
					response := map[string]any{"name": fc["name"], "response": map[string]any{"error": hardtest.ToolError}}
					if id, _ := fc["id"].(string); id != "" {
						response["id"] = id
					}
					responses = append(responses, map[string]any{"functionResponse": response})
				}
				contents = append(contents, map[string]any{"role": "user", "parts": responses})

				req := gemini.WithModel(body, tg.model)
				req["contents"] = contents

				if messageText(post(t, h, tg, req)) == "" {
					t.Errorf("[%s] no text after an error tool result", tg.label)
				}
			}
		})
	}
}

func TestConcurrentStreams(t *testing.T) {
	h := gemini.New(t)

	for _, model := range gemini.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			var wg sync.WaitGroup
			errs := make(chan error, hardtest.Concurrency)

			for i := 0; i < hardtest.Concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()

					events := gemini.PostGeminiSSE(t, h, h.Wingman, model.Name, map[string]any{"contents": user(hardtest.UnicodePrompt)})
					if text := streamedText(events); !strings.Contains(text, "Zürich") {
						errs <- fmt.Errorf("stream %d: %q", i, text)
					}
				}()
			}

			wg.Wait()
			close(errs)

			for err := range errs {
				t.Error(err)
			}
		})
	}
}

// TestModelSwitch replays a first turn with thoughts and signatures from one
// backend into the next one.
func TestModelSwitch(t *testing.T) {
	h := gemini.New(t)

	models := gemini.DefaultModels()

	for i := range models {
		from, to := models[i], models[(i+1)%len(models)]
		if from.Name == to.Name {
			continue
		}

		t.Run(from.Name+"→"+to.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, from.Name)
			h.SkipUnlessConfigured(t, to.Name)

			body := map[string]any{
				"tools":            []any{gemini.WeatherTool},
				"generationConfig": map[string]any{"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingLevel": "low"}},
				"contents":         user("What's the weather in London? Use the tool."),
			}

			first := post(t, h, target{"wingman", h.Wingman, from.Name}, body)
			fcs := functionCalls(first)
			if len(fcs) == 0 {
				t.Fatalf("turn 1 on %s produced no functionCall", from.Name)
			}

			content, _ := candidate(first)["content"].(map[string]any)
			contents := append([]any{}, body["contents"].([]any)...)
			contents = append(contents, map[string]any{"role": "model", "parts": content["parts"]})

			var responses []any
			for _, fc := range fcs {
				response := map[string]any{"name": fc["name"], "response": map[string]any{"result": "Sunny, 22°C"}}
				if id, _ := fc["id"].(string); id != "" {
					response["id"] = id
				}
				responses = append(responses, map[string]any{"functionResponse": response})
			}
			contents = append(contents, map[string]any{"role": "user", "parts": responses})

			req := gemini.WithModel(body, to.Name)
			req["contents"] = contents

			if messageText(post(t, h, target{"wingman", h.Wingman, to.Name}, req)) == "" {
				t.Errorf("turn 2 on %s produced no text", to.Name)
			}
		})
	}
}

func streamedFunctionCalls(events []*harness.SSEEvent) []map[string]any {
	var calls []map[string]any
	for _, e := range events {
		if e.Data == nil {
			continue
		}
		calls = append(calls, functionCalls(e.Data)...)
	}
	return calls
}

func streamedText(events []*harness.SSEEvent) string {
	var b strings.Builder
	for _, e := range events {
		if e.Data == nil {
			continue
		}
		b.WriteString(messageText(e.Data))
	}
	return b.String()
}

func finishReason(events []*harness.SSEEvent) string {
	for _, e := range events {
		if e.Data == nil {
			continue
		}
		if fr, ok := candidate(e.Data)["finishReason"].(string); ok && fr != "" {
			return fr
		}
	}
	return ""
}

func requireNoRepeatedText(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	var streamed string
	for i, e := range events {
		if e.Data == nil {
			continue
		}
		chunk := messageText(e.Data)
		if len(chunk) >= 10 && streamed != "" && strings.Contains(streamed, chunk) {
			t.Fatalf("[%s] chunk %d repeats streamed text: %q", label, i, chunk)
		}
		streamed += chunk
	}
}
