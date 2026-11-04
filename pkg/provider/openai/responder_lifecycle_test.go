package openai

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestResponderReconcilesFinalToolSnapshots(t *testing.T) {
	for _, kind := range []string{"function_call", "custom_tool_call"} {
		for _, placement := range []string{"item done", "response completed"} {
			for _, prefix := range []string{"none", "partial", "complete", "conflicting"} {
				t.Run(fmt.Sprintf("%s/%s/%s", kind, placement, prefix), func(t *testing.T) {
					arguments, field, deltaType := `{"path":"README.md","limit":7}`, "arguments", "response.function_call_arguments.delta"
					if kind == "custom_tool_call" {
						arguments, field, deltaType = "SELECT 1;\n", "input", "response.custom_tool_call_input.delta"
					}
					item := map[string]any{"id": "item_native", "type": kind, "call_id": "call_native", "name": "inspect", "namespace": "helpers", "async": true, "status": "completed", field: arguments}
					var stream strings.Builder
					emit := func(event map[string]any) {
						data, _ := json.Marshal(event)
						fmt.Fprintf(&stream, "data: %s\n\n", data)
					}
					if prefix != "none" {
						emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "item_native", "type": kind, "call_id": "call_native", "name": "inspect", "status": "in_progress", field: ""}})
						part := arguments
						if prefix == "partial" {
							part = arguments[:len(arguments)/2]
						} else if prefix == "conflicting" {
							part = "different streamed input"
						}
						emit(map[string]any{"type": deltaType, "output_index": 0, "item_id": "item_native", "delta": part})
					}
					if placement == "item done" {
						emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
					}
					emit(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{item}}})
					client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String())), Request: r}, nil
					})}
					responder, err := NewResponder("http://upstream.test", "test", WithToken("test"), WithClient(client))
					if err != nil {
						t.Fatal(err)
					}
					var acc provider.CompletionAccumulator
					var runErr error
					for chunk, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("check")}, nil) {
						if err != nil {
							runErr = err
							break
						}
						acc.Add(*chunk)
					}
					calls := acc.Result().Message.ToolCalls()
					if prefix == "conflicting" {
						if runErr == nil || acc.Result().Status == provider.CompletionStatusCompleted || len(calls) != 1 || calls[0].Arguments != "different streamed input" {
							t.Fatalf("contradictory snapshot replaced streamed input: calls=%+v err=%v", calls, runErr)
						}
						return
					}
					if runErr != nil {
						t.Fatal(runErr)
					}
					if len(calls) != 1 || calls[0].ID != "call_native" || calls[0].Name != "inspect" || calls[0].Namespace != "helpers" || !calls[0].Async || calls[0].Arguments != arguments {
						t.Fatalf("final tool snapshot was lost or duplicated: %+v", calls)
					}
					if kind == "custom_tool_call" && calls[0].Kind != provider.ToolKindCustom {
						t.Fatalf("custom tool kind lost: %+v", calls[0])
					}
				})
			}
		}
	}
}

type responderReadError struct{ err error }

func (r responderReadError) Read([]byte) (int, error) { return 0, r.err }

func TestResponderIgnoresTransportErrorsOnlyAfterTerminalEvent(t *testing.T) {
	for _, terminal := range []string{"", "response.completed", "response.incomplete"} {
		for _, visible := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/visible=%t", terminal, visible), func(t *testing.T) {
				text := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"
				if visible {
					text += "data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"delta\":\"partial\"}\n\n"
				}
				if terminal != "" {
					text += fmt.Sprintf("data: {\"type\":%q,\"response\":{\"status\":%q,\"output\":[]}}\n\n", terminal, strings.TrimPrefix(terminal, "response."))
				}
				readErr := errors.New("connection reset after buffered events")
				body := &responderLifecycleBody{Reader: io.MultiReader(strings.NewReader(text), responderReadError{err: readErr})}
				client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: r}, nil
				})}
				responder, err := NewResponder("http://upstream.test", "test", WithToken("test"), WithClient(client))
				if err != nil {
					t.Fatal(err)
				}
				var gotErr error
				for _, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("check")}, nil) {
					gotErr = err
				}
				if terminal == "" && (gotErr == nil || !strings.Contains(gotErr.Error(), readErr.Error())) || terminal != "" && gotErr != nil {
					t.Fatalf("terminal=%q err=%v", terminal, gotErr)
				}
				if !body.closed {
					t.Fatal("upstream body left open")
				}
			})
		}
	}
}

type responderLifecycleTransport func(*http.Request) (*http.Response, error)

func (f responderLifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type responderLifecycleBody struct {
	io.Reader
	closed             bool
	readsAfterTerminal int
}

func (b *responderLifecycleBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.readsAfterTerminal++
	}
	return n, err
}
func (b *responderLifecycleBody) Close() error { b.closed = true; return nil }

func TestResponderPreservesEmptyFinalText(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_empty","type":"message","role":"assistant","phase":"final_answer","status":"in_progress","content":[]}}`,
		`data: {"type":"response.content_part.added","output_index":0,"item_id":"msg_empty","content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[]}}`,
	}, "\n\n") + "\n\n"
	client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: r}, nil
	})}
	r, err := NewResponder("http://upstream.test", "test", WithToken("test"), WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	var acc provider.CompletionAccumulator
	for c, err := range r.Complete(t.Context(), []provider.Message{provider.UserMessage("Go")}, nil) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(*c)
	}
	parts := acc.Result().Message.Content
	if len(parts) != 1 || !parts[0].TextPresent || parts[0].Text != "" || parts[0].MessageID != "msg_empty" || parts[0].Phase != provider.MessagePhaseFinalAnswer {
		t.Fatalf("empty final text lost: %+v", parts)
	}
}

func TestResponderStreamLifecycle(t *testing.T) {
	commentary := "data: " + `{"type":"response.output_text.delta","item_id":"msg_1","delta":"Checking."}` + "\n\n"
	for _, tc := range []struct {
		name, ending  string
		status        provider.CompletionStatus
		unexpectedEOF bool
		stop          []string
		errorMessage  string
		stopConsumer  bool
	}{
		{name: "EOF after commentary", unexpectedEOF: true},
		{name: "done without completion", ending: "data: [DONE]\n\n", unexpectedEOF: true},
		{name: "completed", ending: "data: " + `{"type":"response.completed","response":{"status":"completed","output":[]}}` + "\n\n", status: provider.CompletionStatusCompleted},
		{name: "incomplete", ending: "data: " + `{"type":"response.incomplete","response":{"status":"incomplete","output":[]}}` + "\n\n", status: provider.CompletionStatusIncomplete},
		{name: "requested stop", stop: []string{"Checking."}},
		{name: "consumer stops", stopConsumer: true},
		{name: "failed", ending: "data: " + `{"type":"response.failed","response":{"error":{"code":"server_error","message":"upstream failed"}}}` + "\n\n", errorMessage: "upstream failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &responderLifecycleBody{Reader: strings.NewReader(commentary + tc.ending)}
			client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: r}, nil
			})}
			responder, err := NewResponder("http://upstream.test", "test", WithToken("test"), WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			var runErr error
			var status provider.CompletionStatus
			for chunk, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("Go")}, &provider.CompleteOptions{Stop: tc.stop}) {
				if err != nil {
					runErr = err
					break
				}
				if chunk.Status != "" {
					status = chunk.Status
				}
				if tc.stopConsumer {
					break
				}
			}
			if tc.unexpectedEOF {
				if !errors.Is(runErr, io.ErrUnexpectedEOF) {
					t.Fatalf("error = %v, want unexpected EOF", runErr)
				}
			} else if tc.errorMessage != "" {
				if runErr == nil || !strings.Contains(runErr.Error(), tc.errorMessage) {
					t.Fatalf("error = %v, want %q", runErr, tc.errorMessage)
				}
			} else if runErr != nil {
				t.Fatal(runErr)
			}
			if tc.status != "" && status != tc.status {
				t.Fatalf("status = %q, want %q", status, tc.status)
			}
			if !body.closed {
				t.Error("upstream response body left open")
			}
			if tc.status != "" && body.readsAfterTerminal != 0 {
				t.Error("read past terminal event; an open HTTP body would stall completion")
			}
		})
	}
}
