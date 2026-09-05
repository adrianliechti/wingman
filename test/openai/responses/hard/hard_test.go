// Package hard_test holds the demanding Responses API scenarios: multi-round
// tool loops, parallel calls, streaming reassembly, truncation, forced tool
// choice, images, long context, concurrency, and mid-conversation model
// switches. Every scenario runs against OpenAI with the reference model and
// against Wingman with each configured backend.
package hard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/hardtest"
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
			"location": map[string]any{"type": "string", "description": "The city and country"},
		},
		"required": []string{"location"},
	},
}

var clockTool = map[string]any{
	"type":        "function",
	"name":        "get_time",
	"description": "Get the current time",
	"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
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

	resp, err := h.Client.Post(context.Background(), tg.ep, "/responses", responses.WithModel(body, tg.model))
	if err != nil {
		t.Fatalf("[%s] request failed: %v", tg.label, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", tg.label, resp.StatusCode, string(resp.RawBody))
	}

	return resp.Body
}

func postSSE(t *testing.T, h *openai.Harness, tg target, body map[string]any) []*harness.SSEEvent {
	t.Helper()

	req := responses.WithModel(body, tg.model)
	req["stream"] = true

	events, err := h.Client.PostSSE(context.Background(), tg.ep, "/responses", req)
	if err != nil {
		t.Fatalf("[%s] SSE request failed: %v", tg.label, err)
	}
	if len(events) == 0 {
		t.Fatalf("[%s] no SSE events", tg.label)
	}

	return events
}

func userInput(text string) []any {
	return []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}
}

func messageText(body map[string]any) string {
	var b strings.Builder

	output, _ := body["output"].([]any)
	for _, item := range output {
		m, _ := item.(map[string]any)
		if m["type"] != "message" {
			continue
		}
		content, _ := m["content"].([]any)
		for _, c := range content {
			part, _ := c.(map[string]any)
			if part["type"] == "output_text" {
				text, _ := part["text"].(string)
				b.WriteString(text)
			}
		}
	}

	return b.String()
}

// toolLoop drives a complete tool loop: every function_call in the output is
// answered and the whole output (reasoning items included) is replayed until
// the model answers with text. It returns the number of calls and the answer.
func toolLoop(t *testing.T, h *openai.Harness, tg target, body map[string]any) (int, string) {
	t.Helper()

	input := append([]any{}, body["input"].([]any)...)
	calls := 0

	for round := 0; round < 6; round++ {
		req := responses.WithModel(body, tg.model)
		req["input"] = input

		resp := post(t, h, tg, req)

		output, _ := resp["output"].([]any)
		var results []any

		for _, item := range output {
			m, _ := item.(map[string]any)
			if m["type"] != "function_call" {
				continue
			}

			calls++

			args, _ := m["arguments"].(string)
			name, _ := m["name"].(string)

			results = append(results, map[string]any{
				"type":    "function_call_output",
				"call_id": m["call_id"],
				"output":  hardtest.ToolResult(name, args),
			})
		}

		input = append(input, output...)

		if len(results) == 0 {
			return calls, messageText(resp)
		}

		input = append(input, results...)
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
				"store": false,
				"tools": []any{weatherTool},
				"input": userInput(hardtest.ParallelPrompt),
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
				"store":     false,
				"tools":     []any{weatherTool, clockTool},
				"reasoning": map[string]any{"effort": "low", "summary": "auto"},
				"include":   []string{"reasoning.encrypted_content"},
				"input":     userInput(hardtest.ChainedPrompt),
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
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			for _, tg := range targets(h, model) {
				required := post(t, h, tg, map[string]any{
					"store":       false,
					"tools":       []any{weatherTool},
					"tool_choice": "required",
					"input":       userInput("Say hello."),
				})
				if countItems(required, "function_call") == 0 {
					t.Errorf("[%s] tool_choice required produced no call: %s", tg.label, messageText(required))
				}

				none := post(t, h, tg, map[string]any{
					"store":       false,
					"tools":       []any{weatherTool},
					"tool_choice": "none",
					"input":       userInput("What's the weather in London?"),
				})
				// Claude answers with empty content when the only useful move
				// is the suppressed tool call, so only the absence of a call is
				// asserted.
				if countItems(none, "function_call") != 0 {
					t.Errorf("[%s] tool_choice none still produced a call", tg.label)
				}
			}
		})
	}
}

func TestArgumentlessTool(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"store": false,
				"tools": []any{clockTool},
				"input": userInput("What time is it? Use the tool."),
			}

			for _, tg := range targets(h, model) {
				events := postSSE(t, h, tg, body)

				args, done := streamedArguments(events)
				if args == nil {
					t.Fatalf("[%s] no function call streamed", tg.label)
				}
				for id, a := range args {
					if !json.Valid([]byte(a)) {
						t.Errorf("[%s] arguments for %s are not JSON: %q", tg.label, id, a)
					}
					if done[id] != a {
						t.Errorf("[%s] arguments.done %q differs from deltas %q", tg.label, done[id], a)
					}
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
				"store": false,
				"tools": []any{weatherTool},
				"input": userInput(hardtest.TextThenToolPrompt),
			}

			for _, tg := range targets(h, model) {
				events := postSSE(t, h, tg, body)

				deltas, done := streamedText(events)
				if done != "" && deltas != done {
					t.Errorf("[%s] output_text.delta concatenation %q != output_text.done %q", tg.label, deltas, done)
				}

				args, argsDone := streamedArguments(events)
				if len(args) == 0 {
					t.Fatalf("[%s] no function call streamed", tg.label)
				}
				for id, a := range args {
					if !json.Valid([]byte(a)) || argsDone[id] != a {
						t.Errorf("[%s] arguments for %s: deltas %q done %q", tg.label, id, a, argsDone[id])
					}
				}

				last := events[len(events)-1]
				if typ, _ := last.Data["type"].(string); typ != "response.completed" {
					t.Errorf("[%s] stream ended with %s", tg.label, typ)
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

			body := map[string]any{
				"store": false,
				"input": userInput(hardtest.UnicodePrompt),
			}

			for _, tg := range targets(h, model) {
				deltas, done := streamedText(postSSE(t, h, tg, body))

				if deltas != done {
					t.Errorf("[%s] delta concatenation differs from done text", tg.label)
				}
				hardtest.RequireUnicode(t, tg.label, deltas)
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
				"store":             false,
				"max_output_tokens": 32,
				"input":             userInput(hardtest.EssayPrompt),
			}

			for _, tg := range targets(h, model) {
				resp := post(t, h, tg, body)
				if resp["status"] != "incomplete" {
					t.Errorf("[%s] expected status incomplete, got %v", tg.label, resp["status"])
				}
				details, _ := resp["incomplete_details"].(map[string]any)
				if details["reason"] != "max_output_tokens" {
					t.Errorf("[%s] incomplete_details: %v", tg.label, resp["incomplete_details"])
				}

				events := postSSE(t, h, tg, body)
				last := events[len(events)-1]
				if typ, _ := last.Data["type"].(string); typ != "response.incomplete" {
					t.Errorf("[%s] stream ended with %s, expected response.incomplete", tg.label, typ)
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
				"store": false,
				"input": []any{map[string]any{
					"type": "message",
					"role": "user",
					"content": []any{
						map[string]any{"type": "input_image", "image_url": hardtest.RedSquareDataURL()},
						map[string]any{"type": "input_text", "text": hardtest.ColorPrompt},
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
				"store": false,
				"input": userInput(hardtest.BookPrompt),
				"text": map[string]any{"format": map[string]any{
					"type":   "json_schema",
					"name":   "book",
					"schema": hardtest.BookSchema,
					"strict": true,
				}},
			}

			for _, tg := range targets(h, model) {
				deltas, _ := streamedText(postSSE(t, h, tg, body))
				hardtest.RequireBookJSON(t, tg.label, deltas)
			}
		})
	}
}

func TestLongContextNeedle(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"store": false,
				"input": userInput(hardtest.NeedlePrompt()),
			}

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
				"store": false,
				"tools": []any{weatherTool},
				"input": append(userInput("What's the weather in London?"),
					map[string]any{"type": "function_call", "call_id": "call_err", "name": "get_weather", "arguments": `{"location":"London"}`},
					map[string]any{"type": "function_call_output", "call_id": "call_err", "output": hardtest.ToolError},
				),
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

			body := map[string]any{
				"store": false,
				"input": userInput(hardtest.UnicodePrompt),
			}

			var wg sync.WaitGroup
			errs := make(chan error, hardtest.Concurrency)

			for i := 0; i < hardtest.Concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()

					req := responses.WithModel(body, model.Name)
					req["stream"] = true

					events, err := h.Client.PostSSE(context.Background(), h.Wingman, "/responses", req)
					if err != nil {
						errs <- err
						return
					}

					deltas, done := streamedText(events)
					if deltas == "" || deltas != done {
						errs <- fmt.Errorf("stream %d: text %q done %q", i, deltas, done)
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

// TestModelSwitch replays a full first turn (reasoning items with
// encrypted_content, function call) from one backend into the next one. The
// second backend cannot verify the first one's reasoning state and must drop
// it rather than forward it.
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
				"store":     false,
				"tools":     []any{weatherTool},
				"reasoning": map[string]any{"effort": "low", "summary": "auto"},
				"include":   []string{"reasoning.encrypted_content"},
				"input":     userInput("What's the weather in London? Use the tool."),
			}

			first := post(t, h, target{"wingman", h.Wingman, from.Name}, body)
			output, _ := first["output"].([]any)

			input := append([]any{}, body["input"].([]any)...)
			input = append(input, output...)

			calls := 0
			for _, item := range output {
				m, _ := item.(map[string]any)
				if m["type"] == "function_call" {
					calls++
					input = append(input, map[string]any{
						"type":    "function_call_output",
						"call_id": m["call_id"],
						"output":  "Sunny, 22°C",
					})
				}
			}
			if calls == 0 {
				t.Fatalf("turn 1 on %s produced no function call", from.Name)
			}

			req := responses.WithModel(body, to.Name)
			req["input"] = input

			second := post(t, h, target{"wingman", h.Wingman, to.Name}, req)
			if messageText(second) == "" {
				t.Errorf("turn 2 on %s produced no text", to.Name)
			}
		})
	}
}

func countItems(body map[string]any, itemType string) int {
	n := 0
	output, _ := body["output"].([]any)
	for _, item := range output {
		m, _ := item.(map[string]any)
		if m["type"] == itemType {
			n++
		}
	}
	return n
}

// streamedText returns the concatenated output_text.delta payloads and the
// concatenated output_text.done texts.
func streamedText(events []*harness.SSEEvent) (string, string) {
	var deltas, done strings.Builder

	for _, e := range events {
		if e.Data == nil {
			continue
		}
		switch e.Data["type"] {
		case "response.output_text.delta":
			d, _ := e.Data["delta"].(string)
			deltas.WriteString(d)
		case "response.output_text.done":
			d, _ := e.Data["text"].(string)
			done.WriteString(d)
		}
	}

	return deltas.String(), done.String()
}

// streamedArguments returns per-item concatenated argument deltas and the
// arguments carried by function_call_arguments.done.
func streamedArguments(events []*harness.SSEEvent) (map[string]string, map[string]string) {
	deltas := map[string]string{}
	done := map[string]string{}

	for _, e := range events {
		if e.Data == nil {
			continue
		}
		id, _ := e.Data["item_id"].(string)
		switch e.Data["type"] {
		case "response.function_call_arguments.delta":
			d, _ := e.Data["delta"].(string)
			deltas[id] += d
		case "response.function_call_arguments.done":
			d, _ := e.Data["arguments"].(string)
			done[id] = d
		}
	}

	if len(deltas) == 0 {
		return nil, nil
	}

	return deltas, done
}
