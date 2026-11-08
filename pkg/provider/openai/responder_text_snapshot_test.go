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
