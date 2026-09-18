package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type lifecycleBody struct {
	*strings.Reader
	closed           bool
	readPastTerminal bool
}

func (b *lifecycleBody) Read(p []byte) (int, error) {
	if b.Reader.Len() == 0 {
		b.readPastTerminal = true
	}
	return b.Reader.Read(p)
}
func (b *lifecycleBody) Close() error { b.closed = true; return nil }

func lifecycleEvents(reason string, terminal bool) string {
	events := sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":2,"cache_creation_input_tokens":3}}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"progress"}}`) +
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		sseEvent("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":4}}`, reason))
	if terminal {
		events += sseEvent("message_stop", `{"type":"message_stop"}`)
	}
	return events
}

func TestCompleterNativeLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, reason                                              string
		terminal, rejectYield, rejectStop, cancelPause, wantError bool
		requests                                                  int
		wantReason                                                provider.StopReason
	}{
		{name: "end", reason: "end_turn", terminal: true, requests: 1, wantReason: provider.StopReasonEndTurn},
		{name: "eof", reason: "end_turn", wantError: true, requests: 1},
		{name: "paused EOF", reason: "pause_turn", wantError: true, requests: 1},
		{name: "missing reason", terminal: true, wantError: true, requests: 1},
		{name: "unknown reason", reason: "future_reason", terminal: true, requests: 1, wantReason: "future_reason"},
		{name: "consumer stopped", reason: "pause_turn", terminal: true, rejectYield: true, requests: 1},
		{name: "consumer stopped at pause", reason: "pause_turn", terminal: true, rejectStop: true, requests: 1},
		{name: "pause canceled", reason: "pause_turn", terminal: true, cancelPause: true, wantError: true, requests: 1},
		{name: "pause limit", reason: "pause_turn", terminal: true, wantError: true, requests: maxPauseContinuations + 1, wantReason: provider.StopReasonPauseTurn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []*lifecycleBody
			client := &http.Client{Transport: lifecycleTransport(func(*http.Request) (*http.Response, error) {
				if len(bodies) > maxPauseContinuations {
					t.Fatal("pause loop is unbounded")
				}
				b := &lifecycleBody{Reader: strings.NewReader(lifecycleEvents(tc.reason, tc.terminal))}
				bodies = append(bodies, b)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: b}, nil
			})}
			c, err := NewCompleter("http://anthropic.test", "claude-test", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var runErr error
			var stopReason provider.StopReason
			for delta, err := range c.Complete(ctx, []provider.Message{provider.UserMessage("work")}, nil) {
				runErr = errors.Join(runErr, err)
				if delta != nil && delta.StopReason != "" {
					stopReason = delta.StopReason
				}
				// The native message_stop still carries usage, but its pause
				// reason must be hidden while the completer intends to resume.
				paused := delta != nil && delta.Usage != nil && delta.Message != nil && len(delta.Message.Content) == 0
				if tc.rejectYield || tc.rejectStop && paused {
					break
				}
				if tc.cancelPause && paused {
					cancel()
				}
			}
			if (runErr != nil) != tc.wantError || len(bodies) != tc.requests {
				t.Fatalf("error=%v requests=%d", runErr, len(bodies))
			}
			if stopReason != tc.wantReason {
				t.Errorf("stop reason = %q, want %q", stopReason, tc.wantReason)
			}
			if tc.name == "eof" && !errors.Is(runErr, io.ErrUnexpectedEOF) {
				t.Fatalf("EOF error=%v", runErr)
			}
			if tc.cancelPause && !errors.Is(runErr, context.Canceled) {
				t.Fatalf("cancel error=%v", runErr)
			}
			for _, b := range bodies {
				if !b.closed {
					t.Error("response body not closed")
				}
				if tc.terminal && b.readPastTerminal {
					t.Error("read past message_stop")
				}
			}
		})
	}
}

func TestCompleterResumesPauses(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var body struct {
			Container string `json:"container"`
			Messages  []struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != requests {
			t.Fatalf("request %d lost conversation history: %+v", requests, body.Messages)
		}
		for i, message := range body.Messages {
			wantRole, wantText := "assistant", "progress"
			if i == 0 {
				wantRole, wantText = "user", "work"
			}
			if message.Role != wantRole || len(message.Content) != 1 || message.Content[0].Text != wantText {
				t.Errorf("request %d message %d changed: %+v", requests, i, message)
			}
		}
		if requests > 1 && body.Container != "container_test" {
			t.Errorf("pause lost server container: %q", body.Container)
		}
		reason := "pause_turn"
		if requests == 3 {
			reason = "end_turn"
		}
		events := strings.Replace(lifecycleEvents(reason, true), `"delta":{"stop_reason":`, `"delta":{"container":{"id":"container_test","expires_at":"2030-01-01T00:00:00Z"},"stop_reason":`, 1)
		events = strings.ReplaceAll(events, `"msg_1"`, fmt.Sprintf(`"msg_%d"`, requests))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events))}, nil
	})}
	c, _ := NewCompleter("http://anthropic.test", "claude-test", WithClient(client), WithMaxRetries(0))
	var acc provider.CompletionAccumulator
	var stops []provider.StopReason
	for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("work")}, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if delta.ID != "msg_1" {
			t.Errorf("completion ID changed across continuations: %q", delta.ID)
		}
		if delta.StopReason != "" {
			stops = append(stops, delta.StopReason)
		}
		acc.Add(*delta)
	}
	if len(stops) != 1 || stops[0] != provider.StopReasonEndTurn {
		t.Errorf("stop reasons = %v, want only end_turn", stops)
	}
	result := acc.Result()
	items := result.Message.SplitMessages()
	if len(items) != 3 {
		t.Fatalf("got %d message items, want one per native response", len(items))
	}
	for i, item := range items {
		if item.Text() != "progress" || item.Phase != "" || item.Content[0].MessageID != fmt.Sprintf("msg_%d", i+1) {
			t.Errorf("message item %d = %+v", i, item)
		}
	}
	// Anthropic input_tokens excludes cache reads and creation; provider input
	// includes them, so each request accounts for 10+2+3 input tokens.
	if result.Usage == nil || result.Usage.InputTokens != 45 || result.Usage.OutputTokens != 12 || result.Usage.CacheReadInputTokens != 6 || result.Usage.CacheCreationInputTokens != 9 || result.StopReason != provider.StopReasonEndTurn {
		t.Fatalf("completion=%+v usage=%+v", result, result.Usage)
	}
}

func TestCompleterPreservesThinkingUsageAcrossPauses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		details string
		want    *int
	}{
		{"unknown", "", nil},
		{"zero", `,"output_tokens_details":{"thinking_tokens":0}`, new(0)},
		{"positive", `,"output_tokens_details":{"thinking_tokens":2}`, new(4)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: lifecycleTransport(func(*http.Request) (*http.Response, error) {
				requests++
				reason := "end_turn"
				if requests == 1 {
					reason = "pause_turn"
				}
				// The final usage delta can omit details reported by an earlier delta.
				// They must survive accumulation and contribute to the resumed total.
				events := strings.Replace(lifecycleEvents(reason, true), "event: message_delta\n",
					sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":null},"usage":{"output_tokens":3`+tc.details+`}}`)+"event: message_delta\n", 1)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events))}, nil
			})}
			c, err := NewCompleter("http://anthropic.test", "claude-test", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			var acc provider.CompletionAccumulator
			for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("work")}, nil) {
				if err != nil {
					t.Fatal(err)
				}
				acc.Add(*delta)
			}
			usage := acc.Result().Usage
			if requests != 2 || usage == nil || usage.OutputTokens != 8 {
				t.Fatalf("requests=%d usage=%+v, want usage across 2 messages", requests, usage)
			}
			if usage.HasReasoningTokens() != (tc.want != nil) || tc.want != nil && *usage.ReasoningTokens != *tc.want {
				t.Fatalf("usage=%+v, want reasoning count %v", usage, tc.want)
			}
		})
	}
}
