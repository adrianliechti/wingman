package protocol_test

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCompletionContracts(t *testing.T) {
	for _, mode := range []string{"stop", "length"} {
		for _, stream := range []bool{false, true} {
			for _, p := range protocols(stream) {
				name := mode + "/" + p.name
				if stream {
					name += "/stream"
				}
				t.Run(name, func(t *testing.T) {
					local := gateway(t, textUpstream(t, mode))
					status, headers, raw := post(t, local, p.path, p.body)
					requireStatus(t, status, 200, raw)
					if stream {
						requireType(t, headers, "text/event-stream")
						checkStream(t, p.name, mode, raw)
					} else {
						requireType(t, headers, "application/json")
						checkCompletion(t, p.name, mode, object(t, raw))
					}
				})
			}
		}
	}
}

func checkCompletion(t *testing.T, protocol, mode string, body map[string]any) {
	t.Helper()
	var text, finish, wantFinish, input, output any
	switch protocol {
	case "chat":
		equal(t, "object", body["object"], "chat.completion")
		equal(t, "role", at(body, "choices", 0, "message", "role"), "assistant")
		text, finish = at(body, "choices", 0, "message", "content"), at(body, "choices", 0, "finish_reason")
		wantFinish = mode
		input, output = at(body, "usage", "prompt_tokens"), at(body, "usage", "completion_tokens")
		equal(t, "total_tokens", at(body, "usage", "total_tokens"), float64(12))
	case "responses":
		equal(t, "object", body["object"], "response")
		text, finish, wantFinish = at(body, "output", 0, "content", 0, "text"), body["status"], "completed"
		if mode == "length" {
			wantFinish = "incomplete"
			equal(t, "incomplete reason", at(body, "incomplete_details", "reason"), "max_output_tokens")
		}
		input, output = at(body, "usage", "input_tokens"), at(body, "usage", "output_tokens")
	case "anthropic":
		equal(t, "type", body["type"], "message")
		equal(t, "role", body["role"], "assistant")
		text, finish, wantFinish = at(body, "content", 0, "text"), body["stop_reason"], "end_turn"
		if mode == "length" {
			wantFinish = "max_tokens"
		}
		input, output = at(body, "usage", "input_tokens"), at(body, "usage", "output_tokens")
	case "gemini":
		equal(t, "role", at(body, "candidates", 0, "content", "role"), "model")
		text, finish, wantFinish = at(body, "candidates", 0, "content", "parts", 0, "text"), at(body, "candidates", 0, "finishReason"), "STOP"
		if mode == "length" {
			wantFinish = "MAX_TOKENS"
		}
		input, output = at(body, "usageMetadata", "promptTokenCount"), at(body, "usageMetadata", "candidatesTokenCount")
	}
	equal(t, "text", text, answer)
	equal(t, "termination", finish, wantFinish)
	equal(t, "input tokens", input, float64(5))
	equal(t, "output tokens", output, float64(7))
}

func checkStream(t *testing.T, protocol, mode string, raw []byte) {
	t.Helper()
	events := frames(t, raw)
	var text strings.Builder
	terminal, usage, done, terminalPosition := 0, 0, 0, -1
	for i, e := range events {
		if e.Raw == "[DONE]" {
			done++
			equal(t, "DONE position", i, len(events)-1)
			continue
		}
		body := e.Data
		if body["error"] != nil || body["type"] == "error" {
			t.Fatalf("stream error: %s", raw)
		}
		var delta any
		switch protocol {
		case "chat":
			equal(t, "object", body["object"], "chat.completion.chunk")
			delta = at(body, "choices", 0, "delta", "content")
			if finish := at(body, "choices", 0, "finish_reason"); finish != nil {
				equal(t, "finish_reason", finish, mode)
				terminal++
				terminalPosition = i
			}
			if body["usage"] != nil {
				usage++
				equal(t, "usage position", i, len(events)-2)
				if terminalPosition < 0 || i <= terminalPosition {
					t.Fatal("usage must follow the terminal choice")
				}
				equal(t, "input tokens", at(body, "usage", "prompt_tokens"), float64(5))
				equal(t, "output tokens", at(body, "usage", "completion_tokens"), float64(7))
				equal(t, "total tokens", at(body, "usage", "total_tokens"), float64(12))
			}
		case "responses":
			if i == 0 {
				equal(t, "first event", e.Event, "response.created")
			}
			if e.Event == "response.output_text.delta" {
				delta = body["delta"]
			}
			if e.Event == "response.completed" || e.Event == "response.incomplete" {
				terminal++
				terminalPosition = i
				usage++
				equal(t, "terminal position", i, len(events)-1)
				response, ok := body["response"].(map[string]any)
				if !ok {
					t.Fatalf("missing response: %v", body)
				}
				checkCompletion(t, protocol, mode, response)
			}
		case "anthropic":
			if i == 0 {
				equal(t, "first event", e.Event, "message_start")
			}
			if e.Event == "content_block_delta" {
				delta = at(body, "delta", "text")
			}
			if e.Event == "message_delta" {
				usage++
				want := "end_turn"
				if mode == "length" {
					want = "max_tokens"
				}
				equal(t, "stop_reason", at(body, "delta", "stop_reason"), want)
				equal(t, "output tokens", at(body, "usage", "output_tokens"), float64(7))
			}
			if e.Event == "message_stop" {
				terminal++
				terminalPosition = i
				equal(t, "terminal position", i, len(events)-1)
			}
		case "gemini":
			delta = at(body, "candidates", 0, "content", "parts", 0, "text")
			if finish := at(body, "candidates", 0, "finishReason"); finish != nil && finish != "" {
				terminal++
				terminalPosition = i
				want := "STOP"
				if mode == "length" {
					want = "MAX_TOKENS"
				}
				equal(t, "finishReason", finish, want)
				equal(t, "terminal position", i, len(events)-1)
			}
			if body["usageMetadata"] != nil {
				// Gemini may report usage on intermediate chunks too.
				if i == len(events)-1 {
					usage++
					equal(t, "input tokens", at(body, "usageMetadata", "promptTokenCount"), float64(5))
					equal(t, "output tokens", at(body, "usageMetadata", "candidatesTokenCount"), float64(7))
				}
			}
		}
		if value, ok := delta.(string); ok && value != "" {
			if terminalPosition >= 0 && i > terminalPosition {
				t.Fatal("text after terminal event")
			}
			text.WriteString(value)
		}
	}
	equal(t, "streamed text", text.String(), answer)
	equal(t, "terminal event count", terminal, 1)
	equal(t, "final usage count", usage, 1)
	wantDone := 0
	if protocol == "chat" {
		wantDone = 1
	}
	equal(t, "DONE count", done, wantDone)
}

func TestUpstreamFailureContracts(t *testing.T) {
	for _, mode := range []string{"rate_limit", "error", "truncated", "disconnect", "empty"} {
		for _, stream := range []bool{false, true} {
			for _, p := range protocols(stream) {
				name := mode + "/" + p.name
				if stream {
					name += "/stream"
				}
				t.Run(name, func(t *testing.T) {
					local := gateway(t, textUpstream(t, mode))
					status, headers, raw := post(t, local, p.path, p.body)
					if mode == "rate_limit" {
						requireStatus(t, status, 429, raw)
						equal(t, "Retry-After", headers.Get("Retry-After"), "3")
					}
					if !stream || mode == "rate_limit" || mode == "empty" {
						if status < 400 {
							t.Fatalf("upstream failure returned HTTP %d: %s", status, raw)
						}
						requireType(t, headers, "application/json")
						if at(object(t, raw), "error", "message") == nil {
							t.Fatalf("missing protocol error: %s", raw)
						}
						return
					}
					requireStatus(t, status, 200, raw)
					requireType(t, headers, "text/event-stream")
					errors := 0
					for _, e := range frames(t, raw) {
						if e.Data["error"] != nil || e.Event == "error" || e.Event == "response.failed" {
							errors++
						}
						if e.Event == "response.completed" || e.Event == "message_stop" || at(e.Data, "candidates", 0, "finishReason") == "STOP" || at(e.Data, "choices", 0, "finish_reason") == "stop" {
							t.Fatalf("upstream failure emitted success: %s", raw)
						}
					}
					wantErrors := 1
					if p.name == "responses" {
						wantErrors = 2 // error followed by response.failed
					}
					equal(t, "error event count", errors, wantErrors)
				})
			}
		}
	}
}

func TestChatUsageIsOptIn(t *testing.T) {
	for _, setting := range []string{"omitted", "false", "true"} {
		t.Run(setting, func(t *testing.T) {
			local := gateway(t, textUpstream(t, "stop"))
			p := protocols(true)[0]
			if setting == "omitted" {
				delete(p.body, "stream_options")
			} else {
				p.body["stream_options"] = map[string]any{"include_usage": setting == "true"}
			}
			status, _, raw := post(t, local, p.path, p.body)
			requireStatus(t, status, 200, raw)
			usage := 0
			for _, event := range frames(t, raw) {
				if event.Data["usage"] != nil {
					usage++
				}
			}
			want := 0
			if setting == "true" {
				want = 1
			}
			equal(t, "usage count", usage, want)
		})
	}
}

func TestGeminiJSONArrayStream(t *testing.T) {
	for _, mode := range []string{"stop", "error"} {
		t.Run(mode, func(t *testing.T) {
			local := gateway(t, textUpstream(t, mode))
			p := protocols(true)[3]
			status, headers, raw := post(t, local, strings.TrimSuffix(p.path, "?alt=sse"), p.body)
			requireStatus(t, status, 200, raw)
			requireType(t, headers, "application/json")
			var chunks []map[string]any
			if err := json.Unmarshal(raw, &chunks); err != nil {
				t.Fatalf("invalid JSON array: %v: %s", err, raw)
			}
			if len(chunks) == 0 {
				t.Fatal("empty stream")
			}
			last := chunks[len(chunks)-1]
			if mode == "error" {
				if at(last, "error", "message") == nil {
					t.Fatalf("missing final error: %s", raw)
				}
			} else {
				equal(t, "finishReason", at(last, "candidates", 0, "finishReason"), "STOP")
			}
		})
	}
}

func TestClientCancellationReachesUpstream(t *testing.T) {
	for _, p := range protocols(true) {
		t.Run(p.name, func(t *testing.T) {
			cancelled := make(chan struct{})
			handler := textUpstream(t, "cancel")
			local := gateway(t, func(w http.ResponseWriter, r *http.Request) { handler(w, r); close(cancelled) })
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			body, err := json.Marshal(p.body)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, local.URL+p.path, strings.NewReader(string(body)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := local.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			buf := make([]byte, 1)
			if _, err := io.ReadFull(resp.Body, buf); err != nil {
				t.Fatal(err)
			}
			cancel()
			resp.Body.Close()
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("client cancellation did not reach upstream")
			}
		})
	}
}
