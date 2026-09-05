// Package hard_test holds the demanding Messages API scenarios. Every
// scenario runs against Anthropic with the reference model and against
// Wingman with each configured backend.
package hard_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/harness"
)

var clockTool = map[string]any{
	"name":         "get_time",
	"description":  "Get the current time",
	"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
}

type target struct {
	label string
	ep    harness.Endpoint
	model string
}

func targets(h *anthropic.Harness, model anthropic.Model) []target {
	return []target{
		{"anthropic", h.Anthropic, h.ReferenceModel},
		{"wingman", h.Wingman, model.Name},
	}
}

func post(t *testing.T, h *anthropic.Harness, tg target, body map[string]any) map[string]any {
	t.Helper()

	resp := anthropic.PostMessages(t, h, tg.ep, anthropic.WithModel(body, tg.model))
	if resp.StatusCode != 200 {
		t.Fatalf("[%s] status %d: %s", tg.label, resp.StatusCode, string(resp.RawBody))
	}

	return resp.Body
}

func postSSE(t *testing.T, h *anthropic.Harness, tg target, body map[string]any) []*harness.SSEEvent {
	t.Helper()

	req := anthropic.WithModel(body, tg.model)
	req["stream"] = true

	events := anthropic.PostMessagesSSE(t, h, tg.ep, req)
	if len(events) == 0 {
		t.Fatalf("[%s] no SSE events", tg.label)
	}

	return events
}

func user(text string) []any {
	return []any{map[string]any{"role": "user", "content": text}}
}

func messageText(body map[string]any) string {
	var b strings.Builder

	content, _ := body["content"].([]any)
	for _, block := range content {
		m, _ := block.(map[string]any)
		if m["type"] == "text" {
			text, _ := m["text"].(string)
			b.WriteString(text)
		}
	}

	return b.String()
}

func toolUses(body map[string]any) []map[string]any {
	var uses []map[string]any

	content, _ := body["content"].([]any)
	for _, block := range content {
		m, _ := block.(map[string]any)
		if m["type"] == "tool_use" {
			uses = append(uses, m)
		}
	}

	return uses
}

// toolLoop replays the assistant content verbatim (thinking blocks with
// signatures included) and answers every tool_use until the model answers
// with text.
func toolLoop(t *testing.T, h *anthropic.Harness, tg target, body map[string]any) (int, string) {
	t.Helper()

	messages := append([]any{}, body["messages"].([]any)...)
	calls := 0

	for round := 0; round < 6; round++ {
		req := anthropic.WithModel(body, tg.model)
		req["messages"] = messages

		resp := post(t, h, tg, req)

		uses := toolUses(resp)
		if len(uses) == 0 {
			return calls, messageText(resp)
		}

		messages = append(messages, map[string]any{"role": "assistant", "content": resp["content"]})

		var results []any
		for _, use := range uses {
			calls++

			name, _ := use["name"].(string)
			args, _ := json.Marshal(use["input"])

			results = append(results, map[string]any{
				"type":        "tool_result",
				"tool_use_id": use["id"],
				"content":     hardtest.ToolResult(name, string(args)),
			})
		}

		messages = append(messages, map[string]any{"role": "user", "content": results})
	}

	t.Fatalf("[%s] tool loop did not finish within 6 rounds", tg.label)
	return 0, ""
}

func TestParallelToolCalls(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 2048,
				"tools":      []any{anthropic.WeatherTool},
				"messages":   user(hardtest.ParallelPrompt),
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
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 4096,
				"thinking":   map[string]any{"type": "adaptive"},
				"tools":      []any{anthropic.WeatherTool, clockTool},
				"messages":   user(hardtest.ChainedPrompt),
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
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			for _, tg := range targets(h, model) {
				forced := post(t, h, tg, map[string]any{
					"max_tokens":  1024,
					"tools":       []any{anthropic.WeatherTool},
					"tool_choice": map[string]any{"type": "any"},
					"messages":    user("Say hello."),
				})
				if len(toolUses(forced)) == 0 {
					t.Errorf("[%s] tool_choice any produced no tool_use: %q", tg.label, messageText(forced))
				}
				if forced["stop_reason"] != "tool_use" {
					t.Errorf("[%s] stop_reason %v, expected tool_use", tg.label, forced["stop_reason"])
				}

				none := post(t, h, tg, map[string]any{
					"max_tokens":  1024,
					"tools":       []any{anthropic.WeatherTool},
					"tool_choice": map[string]any{"type": "none"},
					"messages":    user("What's the weather in London?"),
				})
				// Claude answers with empty content when the only useful move
				// is the suppressed tool call, so only the absence of a call is
				// asserted.
				if len(toolUses(none)) != 0 {
					t.Errorf("[%s] tool_choice none still produced a tool_use", tg.label)
				}
			}
		})
	}
}

func TestArgumentlessToolStreaming(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 1024,
				"tools":      []any{clockTool},
				"messages":   user("What time is it? Use the tool."),
			}

			for _, tg := range targets(h, model) {
				calls := streamedToolUses(postSSE(t, h, tg, body))
				if len(calls) == 0 {
					t.Fatalf("[%s] no tool_use streamed", tg.label)
				}
				for _, c := range calls {
					input := c.input
					if input == "" {
						input = "{}"
					}
					if !json.Valid([]byte(input)) || c.name != "get_time" || c.id == "" {
						t.Errorf("[%s] bad streamed tool_use: %+v", tg.label, c)
					}
				}
			}
		})
	}
}

func TestStreamingReassembly(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 1024,
				"tools":      []any{anthropic.WeatherTool},
				"messages":   user(hardtest.TextThenToolPrompt),
			}

			for _, tg := range targets(h, model) {
				events := postSSE(t, h, tg, body)

				calls := streamedToolUses(events)
				if len(calls) == 0 {
					t.Fatalf("[%s] no tool_use streamed", tg.label)
				}
				for _, c := range calls {
					var input map[string]any
					if err := json.Unmarshal([]byte(c.input), &input); err != nil || input["location"] == nil {
						t.Errorf("[%s] reassembled input %q", tg.label, c.input)
					}
				}

				if stop := streamedStopReason(events); stop != "tool_use" {
					t.Errorf("[%s] stop_reason %q, expected tool_use", tg.label, stop)
				}
				requireBalancedBlocks(t, tg.label, events)
			}
		})
	}
}

func TestUnicodeStreaming(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{"max_tokens": 256, "messages": user(hardtest.UnicodePrompt)}

			for _, tg := range targets(h, model) {
				hardtest.RequireUnicode(t, tg.label, streamedText(postSSE(t, h, tg, body)))
			}
		})
	}
}

func TestTruncation(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{"max_tokens": 32, "messages": user(hardtest.EssayPrompt)}

			for _, tg := range targets(h, model) {
				if resp := post(t, h, tg, body); resp["stop_reason"] != "max_tokens" {
					t.Errorf("[%s] stop_reason %v, expected max_tokens", tg.label, resp["stop_reason"])
				}
				if stop := streamedStopReason(postSSE(t, h, tg, body)); stop != "max_tokens" {
					t.Errorf("[%s] streamed stop_reason %q, expected max_tokens", tg.label, stop)
				}
			}
		})
	}
}

func TestStopSequence(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens":     256,
				"stop_sequences": []string{"6"},
				"messages":       user(hardtest.CountingPrompt),
			}

			for _, tg := range targets(h, model) {
				resp := post(t, h, tg, body)
				hardtest.RequireStoppedBeforeSix(t, tg.label, messageText(resp))

				// Backends differ in what they report: Gemini has no
				// stop-sequence finish reason and Bedrock omits the matched
				// value, so the exact stop state is asserted on the reference.
				if tg.label == "anthropic" && (resp["stop_reason"] != "stop_sequence" || resp["stop_sequence"] != "6") {
					t.Errorf("[%s] stop_reason %v stop_sequence %v", tg.label, resp["stop_reason"], resp["stop_sequence"])
				}
			}
		})
	}
}

func TestImageInput(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 64,
				"messages": []any{map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "image", "source": map[string]any{
							"type": "base64", "media_type": "image/png",
							"data": base64.StdEncoding.EncodeToString(hardtest.RedSquarePNG()),
						}},
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
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		if !model.Capabilities.StructuredOutput {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens":    1024,
				"messages":      user(hardtest.BookPrompt),
				"output_config": map[string]any{"format": map[string]any{"type": "json_schema", "schema": hardtest.BookSchema}},
			}

			for _, tg := range targets(h, model) {
				hardtest.RequireBookJSON(t, tg.label, streamedText(postSSE(t, h, tg, body)))
			}
		})
	}
}

func TestLongContextNeedle(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			// backends that think by default spend tokens before answering
			body := map[string]any{"max_tokens": 1024, "messages": user(hardtest.NeedlePrompt())}

			for _, tg := range targets(h, model) {
				hardtest.RequireNeedle(t, tg.label, messageText(post(t, h, tg, body)))
			}
		})
	}
}

func TestErrorToolResult(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 512,
				"tools":      []any{anthropic.WeatherTool},
				"messages": []any{
					map[string]any{"role": "user", "content": "What's the weather in London?"},
					map[string]any{"role": "assistant", "content": []any{map[string]any{
						"type": "tool_use", "id": "toolu_err", "name": "get_weather", "input": map[string]any{"location": "London"},
					}}},
					map[string]any{"role": "user", "content": []any{map[string]any{
						"type": "tool_result", "tool_use_id": "toolu_err", "is_error": true, "content": hardtest.ToolError,
					}}},
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
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			var wg sync.WaitGroup
			errs := make(chan error, hardtest.Concurrency)

			for i := 0; i < hardtest.Concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()

					req := anthropic.WithModel(map[string]any{"max_tokens": 256, "messages": user(hardtest.UnicodePrompt)}, model.Name)
					req["stream"] = true

					events := anthropic.PostMessagesSSE(t, h, h.Wingman, req)
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

type streamedUse struct {
	id, name, input string
}

func streamedToolUses(events []*harness.SSEEvent) []streamedUse {
	byIndex := map[int]*streamedUse{}
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
			if block["type"] == "tool_use" {
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				byIndex[idx] = &streamedUse{id: id, name: name}
				order = append(order, idx)
			}
		case "content_block_delta":
			delta, _ := e.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				if use := byIndex[idx]; use != nil {
					pj, _ := delta["partial_json"].(string)
					use.input += pj
				}
			}
		}
	}

	var uses []streamedUse
	for _, idx := range order {
		uses = append(uses, *byIndex[idx])
	}
	return uses
}

func streamedText(events []*harness.SSEEvent) string {
	var b strings.Builder
	for _, e := range events {
		if e.Data == nil || e.Data["type"] != "content_block_delta" {
			continue
		}
		delta, _ := e.Data["delta"].(map[string]any)
		if delta["type"] == "text_delta" {
			text, _ := delta["text"].(string)
			b.WriteString(text)
		}
	}
	return b.String()
}

func streamedStopReason(events []*harness.SSEEvent) string {
	for _, e := range events {
		if e.Data == nil || e.Data["type"] != "message_delta" {
			continue
		}
		delta, _ := e.Data["delta"].(map[string]any)
		stop, _ := delta["stop_reason"].(string)
		return stop
	}
	return ""
}

// requireBalancedBlocks checks that every content block is started once,
// stopped once, and never receives a delta after its stop.
func requireBalancedBlocks(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	open := map[int]bool{}
	seen := map[int]bool{}

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
			if seen[idx] {
				t.Errorf("[%s] block %d started twice", label, idx)
			}
			seen[idx], open[idx] = true, true
		case "content_block_delta":
			if !open[idx] {
				t.Errorf("[%s] delta for block %d outside start/stop", label, idx)
			}
		case "content_block_stop":
			if !open[idx] {
				t.Errorf("[%s] block %d stopped without being open", label, idx)
			}
			open[idx] = false
		}
	}

	for idx, isOpen := range open {
		if isOpen {
			t.Errorf("[%s] block %d never stopped", label, idx)
		}
	}
}
