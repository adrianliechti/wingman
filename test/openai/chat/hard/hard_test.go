// Package hard_test holds the demanding Chat Completions scenarios. Every
// scenario runs against OpenAI with the reference model and against Wingman
// with each configured backend.
package hard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/chat"
)

var weatherTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "get_weather",
		"description": "Get the current weather for a location",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{"type": "string", "description": "The city and country"},
			},
			"required": []string{"location"},
		},
	},
}

var clockTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "get_time",
		"description": "Get the current time",
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

type target struct {
	label string
	ep    harness.Endpoint
	model string
}

func targets(h *openai.Harness, model openai.Model) []target {
	return []target{
		{"openai", h.OpenAI, h.ReferenceModel},
		{"wingman", h.Wingman, model.Name},
	}
}

func post(t *testing.T, h *openai.Harness, tg target, body map[string]any) map[string]any {
	t.Helper()

	resp, err := h.Client.Post(context.Background(), tg.ep, "/chat/completions", chat.WithModel(body, tg.model))
	if err != nil {
		t.Fatalf("[%s] request failed: %v", tg.label, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", tg.label, resp.StatusCode, string(resp.RawBody))
	}

	return resp.Body
}

// postOrSkip is post for scenarios the reference model rejects outright on
// Chat Completions (GPT-5 models refuse `stop` and function tools combined
// with reasoning_effort there). A 400 from the reference skips that target;
// Wingman must still handle the request on every backend.
func postOrSkip(t *testing.T, h *openai.Harness, tg target, body map[string]any) map[string]any {
	t.Helper()

	resp, err := h.Client.Post(context.Background(), tg.ep, "/chat/completions", chat.WithModel(body, tg.model))
	if err != nil {
		t.Fatalf("[%s] request failed: %v", tg.label, err)
	}
	if resp.StatusCode == 400 && tg.label != "wingman" {
		t.Logf("[%s] reference rejects the request, skipping this target: %s", tg.label, string(resp.RawBody))
		return nil
	}
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", tg.label, resp.StatusCode, string(resp.RawBody))
	}

	return resp.Body
}

func postSSE(t *testing.T, h *openai.Harness, tg target, body map[string]any) []*harness.SSEEvent {
	t.Helper()

	req := chat.WithModel(body, tg.model)
	req["stream"] = true

	events, err := h.Client.PostSSE(context.Background(), tg.ep, "/chat/completions", req)
	if err != nil {
		t.Fatalf("[%s] SSE request failed: %v", tg.label, err)
	}
	if len(events) == 0 {
		t.Fatalf("[%s] no SSE events", tg.label)
	}

	return events
}

func user(text string) []any {
	return []any{map[string]any{"role": "user", "content": text}}
}

func choice(body map[string]any) map[string]any {
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		return map[string]any{}
	}
	c, _ := choices[0].(map[string]any)
	return c
}

func messageText(body map[string]any) string {
	message, _ := choice(body)["message"].(map[string]any)
	text, _ := message["content"].(string)
	return text
}

func toolCalls(body map[string]any) []map[string]any {
	message, _ := choice(body)["message"].(map[string]any)
	raw, _ := message["tool_calls"].([]any)

	var calls []map[string]any
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			calls = append(calls, m)
		}
	}
	return calls
}

// toolLoop drives a complete tool loop, replaying the assistant message and
// answering every call until the model answers with text.
func toolLoop(t *testing.T, h *openai.Harness, tg target, body map[string]any) (int, string) {
	t.Helper()

	messages := append([]any{}, body["messages"].([]any)...)
	calls := 0

	for round := 0; round < 6; round++ {
		req := chat.WithModel(body, tg.model)
		req["messages"] = messages

		resp := postOrSkip(t, h, tg, req)
		if resp == nil {
			return -1, ""
		}

		message, _ := choice(resp)["message"].(map[string]any)
		messages = append(messages, message)

		tcs := toolCalls(resp)
		if len(tcs) == 0 {
			return calls, messageText(resp)
		}

		for _, tc := range tcs {
			calls++

			fn, _ := tc["function"].(map[string]any)
			name, _ := fn["name"].(string)
			args, _ := fn["arguments"].(string)

			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": tc["id"],
				"content":      hardtest.ToolResult(name, args),
			})
		}
	}

	t.Fatalf("[%s] tool loop did not finish within 6 rounds", tg.label)
	return 0, ""
}

func TestParallelToolCalls(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":    []any{weatherTool},
				"messages": user(hardtest.ParallelPrompt),
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

func TestToolLoopWithReasoning(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":            []any{weatherTool, clockTool},
				"reasoning_effort": "low",
				"messages":         user(hardtest.ChainedPrompt),
			}

			for _, tg := range targets(h, model) {
				calls, answer := toolLoop(t, h, tg, body)
				if calls < 0 {
					continue
				}

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
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			for _, tg := range targets(h, model) {
				required := post(t, h, tg, map[string]any{
					"tools":       []any{weatherTool},
					"tool_choice": "required",
					"messages":    user("Say hello."),
				})
				if len(toolCalls(required)) == 0 {
					t.Errorf("[%s] tool_choice required produced no call: %q", tg.label, messageText(required))
				}
				if fr, _ := choice(required)["finish_reason"].(string); fr != "tool_calls" {
					t.Errorf("[%s] finish_reason %q, expected tool_calls", tg.label, fr)
				}

				none := post(t, h, tg, map[string]any{
					"tools":       []any{weatherTool},
					"tool_choice": "none",
					"messages":    user("What's the weather in London?"),
				})
				// Claude answers with empty content when the only useful move
				// is the suppressed tool call, so only the absence of a call is
				// asserted.
				if len(toolCalls(none)) != 0 {
					t.Errorf("[%s] tool_choice none still produced a call", tg.label)
				}
			}
		})
	}
}

func TestArgumentlessToolStreaming(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":    []any{clockTool},
				"messages": user("What time is it? Use the tool."),
			}

			for _, tg := range targets(h, model) {
				events := postSSE(t, h, tg, body)

				calls := streamedToolCalls(events)
				if len(calls) == 0 {
					t.Fatalf("[%s] no tool call streamed", tg.label)
				}
				for _, c := range calls {
					if !json.Valid([]byte(c.arguments)) {
						t.Errorf("[%s] arguments are not JSON: %q", tg.label, c.arguments)
					}
					if c.name != "get_time" || c.id == "" {
						t.Errorf("[%s] call missing name or id: %+v", tg.label, c)
					}
				}
				if fr := finishReason(events); fr != "tool_calls" {
					t.Errorf("[%s] finish_reason %q", tg.label, fr)
				}
			}
		})
	}
}

func TestStreamingReassembly(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools":    []any{weatherTool},
				"messages": user(hardtest.TextThenToolPrompt),
			}

			for _, tg := range targets(h, model) {
				events := postSSE(t, h, tg, body)

				calls := streamedToolCalls(events)
				if len(calls) == 0 {
					t.Fatalf("[%s] no tool call streamed", tg.label)
				}
				for _, c := range calls {
					var args map[string]any
					if err := json.Unmarshal([]byte(c.arguments), &args); err != nil || args["location"] == nil {
						t.Errorf("[%s] reassembled arguments %q", tg.label, c.arguments)
					}
				}
				if last := events[len(events)-1]; last.Raw != "[DONE]" {
					t.Errorf("[%s] stream did not end with [DONE]", tg.label)
				}
			}
		})
	}
}

func TestUnicodeStreaming(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{"messages": user(hardtest.UnicodePrompt)}

			for _, tg := range targets(h, model) {
				hardtest.RequireUnicode(t, tg.label, streamedText(postSSE(t, h, tg, body)))
			}
		})
	}
}

func TestTruncation(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_completion_tokens": 32,
				"messages":              user(hardtest.EssayPrompt),
			}

			for _, tg := range targets(h, model) {
				if fr, _ := choice(post(t, h, tg, body))["finish_reason"].(string); fr != "length" {
					t.Errorf("[%s] finish_reason %q, expected length", tg.label, fr)
				}
				if fr := finishReason(postSSE(t, h, tg, body)); fr != "length" {
					t.Errorf("[%s] streamed finish_reason %q, expected length", tg.label, fr)
				}
			}
		})
	}
}

func TestStopSequence(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"stop":     []string{"6"},
				"messages": user(hardtest.CountingPrompt),
			}

			for _, tg := range targets(h, model) {
				resp := postOrSkip(t, h, tg, body)
				if resp == nil {
					continue
				}
				hardtest.RequireStoppedBeforeSix(t, tg.label, messageText(resp))
				if fr, _ := choice(resp)["finish_reason"].(string); fr != "stop" {
					t.Errorf("[%s] finish_reason %q, expected stop", tg.label, fr)
				}
			}
		})
	}
}

func TestImageInput(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"messages": []any{map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "image_url", "image_url": map[string]any{"url": hardtest.RedSquareDataURL()}},
						map[string]any{"type": "text", "text": hardtest.ColorPrompt},
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
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.StructuredOutput {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"messages": user(hardtest.BookPrompt),
				"response_format": map[string]any{
					"type":        "json_schema",
					"json_schema": map[string]any{"name": "book", "schema": hardtest.BookSchema, "strict": true},
				},
			}

			for _, tg := range targets(h, model) {
				hardtest.RequireBookJSON(t, tg.label, streamedText(postSSE(t, h, tg, body)))
			}
		})
	}
}

func TestLongContextNeedle(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{"messages": user(hardtest.NeedlePrompt())}

			for _, tg := range targets(h, model) {
				hardtest.RequireNeedle(t, tg.label, messageText(post(t, h, tg, body)))
			}
		})
	}
}

func TestErrorToolResult(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"tools": []any{weatherTool},
				"messages": []any{
					map[string]any{"role": "user", "content": "What's the weather in London?"},
					map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
						"id": "call_err", "type": "function",
						"function": map[string]any{"name": "get_weather", "arguments": `{"location":"London"}`},
					}}},
					map[string]any{"role": "tool", "tool_call_id": "call_err", "content": hardtest.ToolError},
				},
			}

			for _, tg := range targets(h, model) {
				if messageText(post(t, h, tg, body)) == "" {
					t.Errorf("[%s] no text after an error tool result", tg.label)
				}
			}
		})
	}
}

func TestConcurrentStreams(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			var wg sync.WaitGroup
			errs := make(chan error, hardtest.Concurrency)

			for i := 0; i < hardtest.Concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()

					req := chat.WithModel(map[string]any{"messages": user(hardtest.UnicodePrompt)}, model.Name)
					req["stream"] = true

					events, err := h.Client.PostSSE(context.Background(), h.Wingman, "/chat/completions", req)
					if err != nil {
						errs <- err
						return
					}
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

// TestModelSwitch answers a tool call issued by one backend on the next one.
func TestModelSwitch(t *testing.T) {
	h := openai.New(t)

	models := openai.DefaultModels()

	for i := range models {
		from, to := models[i], models[(i+1)%len(models)]
		if from.Name == to.Name {
			continue
		}

		t.Run(from.Name+"→"+to.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, from.Name)
			h.SkipUnlessConfigured(t, to.Name)

			body := map[string]any{
				"tools":    []any{weatherTool},
				"messages": user("What's the weather in London? Use the tool."),
			}

			first := post(t, h, target{"wingman", h.Wingman, from.Name}, body)
			calls := toolCalls(first)
			if len(calls) == 0 {
				t.Fatalf("turn 1 on %s produced no tool call", from.Name)
			}

			message, _ := choice(first)["message"].(map[string]any)
			messages := append(append([]any{}, body["messages"].([]any)...), message)
			for _, c := range calls {
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": c["id"], "content": "Sunny, 22°C"})
			}

			req := chat.WithModel(body, to.Name)
			req["messages"] = messages

			if messageText(post(t, h, target{"wingman", h.Wingman, to.Name}, req)) == "" {
				t.Errorf("turn 2 on %s produced no text", to.Name)
			}
		})
	}
}

type streamedCall struct {
	id, name, arguments string
}

// streamedToolCalls reassembles tool calls from indexed deltas the way SDK
// clients do: id, type and name from the first chunk, arguments appended.
func streamedToolCalls(events []*harness.SSEEvent) []streamedCall {
	byIndex := map[int]*streamedCall{}

	for _, e := range events {
		if e.Data == nil {
			continue
		}
		delta, _ := choice(e.Data)["delta"].(map[string]any)
		raw, _ := delta["tool_calls"].([]any)
		for _, tc := range raw {
			m, _ := tc.(map[string]any)
			idx, _ := m["index"].(float64)
			call := byIndex[int(idx)]
			if call == nil {
				call = &streamedCall{}
				byIndex[int(idx)] = call
			}
			if id, _ := m["id"].(string); id != "" {
				call.id = id
			}
			fn, _ := m["function"].(map[string]any)
			if name, _ := fn["name"].(string); name != "" {
				call.name = name
			}
			args, _ := fn["arguments"].(string)
			call.arguments += args
		}
	}

	keys := make([]int, 0, len(byIndex))
	for k := range byIndex {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var calls []streamedCall
	for _, k := range keys {
		calls = append(calls, *byIndex[k])
	}
	return calls
}

func streamedText(events []*harness.SSEEvent) string {
	var b strings.Builder
	for _, e := range events {
		if e.Data == nil {
			continue
		}
		delta, _ := choice(e.Data)["delta"].(map[string]any)
		text, _ := delta["content"].(string)
		b.WriteString(text)
	}
	return b.String()
}

func finishReason(events []*harness.SSEEvent) string {
	for _, e := range events {
		if e.Data == nil {
			continue
		}
		if fr, ok := choice(e.Data)["finish_reason"].(string); ok && fr != "" {
			return fr
		}
	}
	return ""
}
