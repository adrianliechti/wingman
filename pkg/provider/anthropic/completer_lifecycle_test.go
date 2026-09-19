package anthropic

import (
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
		name, reason          string
		terminal, rejectYield bool
		wantError             bool
		wantReason            provider.StopReason
	}{
		{name: "end", reason: "end_turn", terminal: true, wantReason: provider.StopReasonEndTurn},
		{name: "eof", reason: "end_turn", wantError: true},
		{name: "paused EOF", reason: "pause_turn", wantError: true},
		{name: "missing reason", terminal: true, wantError: true},
		{name: "unknown reason", reason: "future_reason", terminal: true, wantReason: "future_reason"},
		// A paused turn is passed through; the caller resends to continue.
		{name: "paused", reason: "pause_turn", terminal: true, wantReason: provider.StopReasonPauseTurn},
		{name: "consumer stopped", reason: "end_turn", terminal: true, rejectYield: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []*lifecycleBody
			client := &http.Client{Transport: lifecycleTransport(func(*http.Request) (*http.Response, error) {
				b := &lifecycleBody{Reader: strings.NewReader(lifecycleEvents(tc.reason, tc.terminal))}
				bodies = append(bodies, b)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: b}, nil
			})}
			c, err := NewCompleter("http://anthropic.test", "claude-test", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			var runErr error
			var stopReason provider.StopReason
			for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("work")}, nil) {
				runErr = errors.Join(runErr, err)
				if delta != nil && delta.StopReason != "" {
					stopReason = delta.StopReason
				}
				if tc.rejectYield {
					break
				}
			}
			if (runErr != nil) != tc.wantError || len(bodies) != 1 {
				t.Fatalf("error=%v requests=%d", runErr, len(bodies))
			}
			if stopReason != tc.wantReason {
				t.Errorf("stop reason = %q, want %q", stopReason, tc.wantReason)
			}
			if tc.name == "eof" && !errors.Is(runErr, io.ErrUnexpectedEOF) {
				t.Fatalf("EOF error=%v", runErr)
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
