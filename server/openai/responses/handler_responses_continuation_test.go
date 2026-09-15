package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
)

func TestResponsesPreservePauseContinuationOrder(t *testing.T) {
	for _, tc := range []struct {
		stream         bool
		reason, status string
	}{
		{false, "end_turn", "completed"},
		{true, "end_turn", "completed"},
		{false, "max_tokens", "incomplete"},
		{true, "max_tokens", "incomplete"},
	} {
		t.Run(fmt.Sprintf("%s/stream=%t", tc.reason, tc.stream), func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if requests > 2 {
					t.Fatal("unexpected continuation")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(continuationEvents(requests, tc.reason))), Request: r}, nil
			})}
			completer, err := anthropic.NewCompleter("http://anthropic.test", "claude-test", anthropic.WithClient(client), anthropic.WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("native", completer)
			rec := httptest.NewRecorder()
			body := fmt.Sprintf(`{"model":"native","input":"work","stream":%t,"include":["reasoning.encrypted_content"]}`, tc.stream)
			New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
			if rec.Code != http.StatusOK || requests != 2 {
				t.Fatalf("HTTP %d, requests=%d: %s", rec.Code, requests, rec.Body.String())
			}

			type item struct {
				ID, Type, Status string
				Phase            *string
				Content          []struct{ Type, Text string }
				EncryptedContent string `json:"encrypted_content"`
			}
			var result struct {
				ID     string
				Status string
				Output []item
			}
			added, done := map[int]item{}, map[int]item{}
			if tc.stream {
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event struct {
						Type        string
						OutputIndex int `json:"output_index"`
						Item        item
						Response    json.RawMessage
					}
					if err := json.Unmarshal([]byte(line[6:]), &event); err != nil {
						t.Fatal(err)
					}
					switch event.Type {
					case "response.created", "response.in_progress":
						var response struct{ ID string }
						if err := json.Unmarshal(event.Response, &response); err != nil || response.ID != "msg_1" {
							t.Fatalf("lost first native completion ID in %s: %s (%v)", event.Type, event.Response, err)
						}
					case "response.output_item.added":
						added[event.OutputIndex] = event.Item
					case "response.output_item.done":
						done[event.OutputIndex] = event.Item
					case "response.completed", "response.incomplete":
						if err := json.Unmarshal(event.Response, &result); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.ID != "msg_1" || result.Status != tc.status || len(result.Output) != 3 {
				t.Fatalf("response = %+v", result)
			}
			message, reasoning, answer := result.Output[0], result.Output[1], result.Output[2]
			if message.ID != "msg_1" || message.Type != "message" || message.Phase != nil || message.Status != "completed" || len(message.Content) != 1 || message.Content[0].Text != "Working. " {
				t.Fatalf("first item = %+v, want the completed unphased message", message)
			}
			if reasoning.Type != "reasoning" || reasoning.Status != "completed" || reasoning.EncryptedContent != "SIG_2" || len(reasoning.Content) != 1 || reasoning.Content[0].Text != "checking result" {
				t.Fatalf("second item = %+v, want the resumed reasoning", reasoning)
			}
			if answer.ID != "msg_2" || answer.Type != "message" || answer.Phase != nil || answer.Status != tc.status || len(answer.Content) != 1 || answer.Content[0].Text != "Done." {
				t.Fatalf("third item = %+v, want the resumed answer", answer)
			}
			if tc.stream {
				if len(added) != len(result.Output) || len(done) != len(result.Output) {
					t.Fatalf("incomplete item lifecycle: added=%v, done=%v", added, done)
				}
				for i, final := range result.Output {
					if added[i].ID != final.ID || added[i].Type != final.Type || !reflect.DeepEqual(done[i], final) {
						t.Errorf("output_index %d changed: added=%+v, done=%+v, final=%+v", i, added[i], done[i], final)
					}
				}
			}
		})
	}
}

// The first response pauses after text; the continuation starts with signed
// thinking. Reasoning therefore cannot be moved ahead of every message item.
func continuationEvents(turn int, stopReason string) string {
	var events strings.Builder
	emit := func(kind, data string) {
		fmt.Fprintf(&events, "event: %s\ndata: %s\n\n", kind, data)
	}
	emit("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_%d","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, turn))
	index, text, reason := 0, "Working. ", "pause_turn"
	if turn == 2 {
		emit("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
		emit("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"checking result"}}`)
		emit("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG_2"}}`)
		emit("content_block_stop", `{"type":"content_block_stop","index":0}`)
		index, text, reason = 1, "Done.", stopReason
	}
	emit("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index))
	emit("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%q}}`, index, text))
	emit("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
	emit("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":4}}`, reason))
	emit("message_stop", `{"type":"message_stop"}`)
	return events.String()
}
