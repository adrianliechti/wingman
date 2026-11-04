package responses

import (
	"context"
	"encoding/json/v2"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
)

type emptyFinalCompleter struct{}

func (emptyFinalCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{Status: provider.CompletionStatusCompleted, Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{ID: "call_probe", Name: "record_probe", Arguments: `{"value":"ok"}`, Async: true}),
			{MessageID: "msg_empty", Phase: provider.MessagePhaseFinalAnswer, TextPresent: true},
		}}}, nil)
	}
}

func TestResponsesKeepEmptyFinalMessageAfterAsyncCall(t *testing.T) {
	check := func(t *testing.T, response map[string]any) {
		t.Helper()
		output, _ := response["output"].([]any)
		if len(output) != 2 {
			t.Fatalf("output = %#v, want async call and empty final message", output)
		}
		call, _ := output[0].(map[string]any)
		message, _ := output[1].(map[string]any)
		if call["type"] != "function_call" || call["async"] != true || message["type"] != "message" || message["id"] != "msg_empty" || message["phase"] != "final_answer" {
			t.Fatalf("output metadata changed: %#v", output)
		}
		parts, _ := message["content"].([]any)
		if len(parts) != 1 {
			t.Fatalf("empty text part lost: %#v", message)
		}
		part, _ := parts[0].(map[string]any)
		if part["type"] != "output_text" || part["text"] != "" {
			t.Fatalf("empty text part changed: %#v", part)
		}
	}
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("test", emptyFinalCompleter{})
			body, _ := json.Marshal(map[string]any{"model": "test", "input": "probe", "stream": stream})
			rec := httptest.NewRecorder()
			New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(string(body))))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if !stream {
				var response map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				check(t, response)
				return
			}
			events, err := harness.ParseSSE(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			completed := false
			itemDone := false
			for _, event := range events {
				if event.Event == "response.output_item.done" {
					if item, _ := event.Data["item"].(map[string]any); item["id"] == "msg_empty" {
						itemDone = true
					}
				}
				if event.Event == "response.completed" {
					response, _ := event.Data["response"].(map[string]any)
					check(t, response)
					completed = true
				}
			}
			if !itemDone {
				t.Fatal("empty message item was added but never done")
			}
			if !completed {
				t.Fatal("missing completed response")
			}
		})
	}
}
