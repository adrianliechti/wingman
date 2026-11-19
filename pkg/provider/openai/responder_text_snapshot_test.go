package openai

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestResponderTextSnapshots(t *testing.T) {
	for _, placement := range []string{"text done", "part done", "item done", "completed", "all"} {
		for _, prefix := range []string{"", "Hello", "Hello 世界 👋", "Draft"} {
			t.Run(fmt.Sprintf("%s/%s", placement, prefix), func(t *testing.T) {
				final := "Hello 世界 👋"
				item := map[string]any{"type": "message", "id": "msg", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": final, "annotations": []any{}}}}
				var stream strings.Builder
				emit := func(event map[string]any) { data, _ := json.Marshal(event); fmt.Fprintf(&stream, "data: %s\n\n", data) }
				emit(map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": "msg", "type": "message", "role": "assistant", "phase": "final_answer", "content": []any{}}})
				if prefix != "" {
					emit(map[string]any{"type": "response.output_text.delta", "item_id": "msg", "content_index": 0, "delta": prefix})
				}
				if placement == "text done" || placement == "all" {
					emit(map[string]any{"type": "response.output_text.done", "item_id": "msg", "content_index": 0, "text": final})
				}
				if placement == "part done" || placement == "all" {
					emit(map[string]any{"type": "response.content_part.done", "item_id": "msg", "content_index": 0, "part": item["content"].([]any)[0]})
				}
				if placement == "item done" || placement == "all" {
					emit(map[string]any{"type": "response.output_item.done", "item": item})
				}
				emit(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{item}}})
				result, err := textSnapshotResult(t, stream.String())
				if prefix == "Draft" {
					if err == nil || result.Status == provider.CompletionStatusCompleted {
						t.Fatal("contradictory text marked complete")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if result.Text() != final || len(result.Message.Content) != 1 || result.Message.Content[0].Phase != provider.MessagePhaseFinalAnswer {
					t.Fatalf("lost or duplicated snapshot: %+v", result.Message)
				}
			})
		}
	}
}

func TestResponderFinalOnlyMessagesAndRepeatedSnapshots(t *testing.T) {
	stream := `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"commentary","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Working"}]},{"type":"message","id":"final","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Hello "},{"type":"output_text","text":"world"}]}]}}` + "\n\n"
	result, err := textSnapshotResult(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text() != "Hello world" || len(result.Message.Content) != 2 || result.Message.Content[0].Phase != provider.MessagePhaseCommentary {
		t.Fatalf("phases/parts lost: %+v", result.Message)
	}
}

func TestResponderRejectsLateBackfillIntoClosedMessage(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","item_id":"first","delta":"Hello"}`,
		`data: {"type":"response.output_text.delta","item_id":"second","delta":"World"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"first","role":"assistant","content":[{"type":"output_text","text":"Hello!"}]}]}}`,
	}, "\n\n") + "\n\n"
	result, err := textSnapshotResult(t, stream)
	if err == nil || result.Status == provider.CompletionStatusCompleted {
		t.Fatal("closed message silently rewritten")
	}
}

func textSnapshotResult(t *testing.T, stream string) (*provider.Completion, error) {
	t.Helper()
	client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: r}, nil
	})}
	responder, err := NewResponder("http://upstream.test", "test", WithToken("test"), WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	var acc provider.CompletionAccumulator
	for chunk, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("check")}, nil) {
		if err != nil {
			return acc.Result(), err
		}
		acc.Add(*chunk)
	}
	return acc.Result(), nil
}

func TestResponderRepeatedFinalSnapshotsDoNotReopenMessages(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","item_id":"first","delta":"One"}`,
		`data: {"type":"response.output_text.delta","item_id":"second","delta":"Two"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"first","role":"assistant","content":[{"type":"output_text","text":"One"}]},{"type":"message","id":"second","role":"assistant","content":[{"type":"output_text","text":"Two"}]}]}}`,
	}, "\n\n") + "\n\n"
	result, err := textSnapshotResult(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.Content) != 2 || result.Message.Content[0].Text != "One" || result.Message.Content[1].Text != "Two" {
		t.Fatalf("reopened finalized message: %+v", result.Message)
	}
}

func TestResponderIncompleteOutput(t *testing.T) {
	for _, reason := range []string{"content_filter", "max_output_tokens"} {
		t.Run(reason, func(t *testing.T) {
			stream := strings.Join([]string{
				`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs","summary_index":0,"delta":"Part"}`,
				`data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"` + reason + `"},"output":[{"type":"reasoning","id":"rs","summary":[{"type":"summary_text","text":"Partial summary"}],"encrypted_content":"opaque"},{"type":"message","id":"msg","role":"assistant","content":[{"type":"output_text","text":"partial answer"},{"type":"refusal","refusal":"declined"}]},{"type":"function_call","id":"fc","call_id":"call","name":"lookup","arguments":"{"}]}}`,
			}, "\n\n") + "\n\n"
			c, err := textSnapshotResult(t, stream)
			if err != nil {
				t.Fatal(err)
			}
			if c.Status != provider.CompletionStatusIncomplete || (c.StopReason == provider.StopReasonRefusal) != (reason == "content_filter") {
				t.Fatalf("wrong completion boundary: %s/%s", c.Status, c.StopReason)
			}
			if c.Message.Text() != "partial answer" || c.Message.Refusal() != "declined" || len(c.Message.Content) != 4 {
				t.Fatalf("partial output changed: %+v", c.Message)
			}
			r := c.Message.Content[0].Reasoning
			if r == nil || r.Signature != "opaque" || r.SummaryText() != "Partial summary" {
				t.Fatalf("reasoning snapshot changed: %+v", r)
			}
			if calls := c.Message.ToolCalls(); len(calls) != 1 || calls[0].Arguments != "{" {
				t.Fatalf("truncated arguments changed: %+v", calls)
			}
		})
	}
}

func TestResponderRefusalSnapshots(t *testing.T) {
	const item = `{"type":"message","id":"msg","role":"assistant","content":[{"type":"refusal","refusal":"declined"}]}`
	for _, done := range []string{
		`{"type":"response.refusal.done","item_id":"msg","refusal":"declined"}`,
		`{"type":"response.content_part.done","item_id":"msg","part":{"type":"refusal","refusal":"declined"}}`,
		`{"type":"response.output_item.done","item":` + item + `}`,
	} {
		stream := "data: " + `{"type":"response.refusal.delta","item_id":"msg","delta":"dec"}` + "\n\ndata: " + done +
			"\n\ndata: " + `{"type":"response.completed","response":{"status":"completed","output":[` + item + `]}}` + "\n\n"
		c, err := textSnapshotResult(t, stream)
		if err != nil || c.Message.Refusal() != "declined" || len(c.Message.Content) != 1 || c.Status != provider.CompletionStatusCompleted {
			t.Fatalf("refusal snapshot: %+v, %v", c, err)
		}
	}
}

func TestResponderArgumentsDoneWithoutItemDone(t *testing.T) {
	for _, tc := range []struct{ item, event, field string }{
		{"function_call", "function_call_arguments", "arguments"},
		{"custom_tool_call", "custom_tool_call_input", "input"},
	} {
		t.Run(tc.item, func(t *testing.T) {
			stream := fmt.Sprintf("data: "+`{"type":"response.output_item.added","item":{"type":"%s","id":"item","call_id":"call","name":"inspect"}}`+
				"\n\ndata: "+`{"type":"response.%s.delta","item_id":"item","delta":"a"}`+
				"\n\ndata: "+`{"type":"response.%s.done","item_id":"item","%s":"abc"}`+
				"\n\ndata: "+`{"type":"response.completed","response":{"status":"completed"}}`+"\n\n", tc.item, tc.event, tc.event, tc.field)
			c, err := textSnapshotResult(t, stream)
			if err != nil {
				t.Fatal(err)
			}
			if calls := c.Message.ToolCalls(); len(calls) != 1 || calls[0].ID != "call" || calls[0].Arguments != "abc" {
				t.Fatalf("arguments.done changed: %+v", calls)
			}
		})
	}
}
