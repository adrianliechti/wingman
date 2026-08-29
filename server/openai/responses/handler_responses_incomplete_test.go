package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

const incompleteModel = "incomplete-test-model"

// truncatedToolCallCompleter streams a tool call whose arguments are cut off
// mid-string, then reports the turn as truncated — the shape a backend
// produces when the response hits max_tokens while writing a large payload.
type truncatedToolCallCompleter struct {
	callID   string
	callName string
	partial  string
}

func (c truncatedToolCallCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if !yield(&provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(provider.ToolCall{ID: c.callID, Name: c.callName}),
				},
			},
		}, nil) {
			return
		}

		if !yield(&provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(provider.ToolCall{ID: c.callID, Name: c.callName, Arguments: c.partial}),
				},
			},
		}, nil) {
			return
		}

		yield(&provider.Completion{Status: provider.CompletionStatusIncomplete}, nil)
	}
}

func streamTruncatedCall(t *testing.T) string {
	t.Helper()

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(incompleteModel, truncatedToolCallCompleter{
		callID:   "call_trunc",
		callName: "create_file",
		partial:  `{"path": "/etl/pipeline.py"`,
	})

	body := []byte(`{
		"model": "` + incompleteModel + `",
		"stream": true,
		"tools": [{"type":"function","name":"create_file","parameters":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}}],
		"input": "write the file"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	return rec.Body.String()
}

// A client cannot tell a truncated call from a malformed one unless the stream
// says so. Both signals are load-bearing: the item's own status, and the
// terminal response.incomplete carrying incomplete_details.reason. Without
// them a repaired-but-incomplete argument object looks like the model simply
// omitted a required field.
func TestStreamMarksTruncatedToolCallItemIncomplete(t *testing.T) {
	stream := streamTruncatedCall(t)

	var item map[string]any

	for _, block := range strings.Split(stream, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			var event map[string]any

			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
				continue
			}

			if event["type"] != "response.output_item.done" {
				continue
			}

			if candidate, _ := event["item"].(map[string]any); candidate["type"] == "function_call" {
				item = candidate
			}
		}
	}

	if item == nil {
		t.Fatalf("no function_call output_item.done in stream\n%s", stream)
	}

	if item["status"] != "incomplete" {
		t.Errorf("item status = %v, want incomplete", item["status"])
	}

	// The partial arguments must still be delivered — a client that repairs
	// them can at least show what the model got through.
	if args, _ := item["arguments"].(string); args != `{"path": "/etl/pipeline.py"` {
		t.Errorf("arguments = %q, want the partial prefix", args)
	}
}

func TestStreamReportsMaxOutputTokensOnTruncation(t *testing.T) {
	stream := streamTruncatedCall(t)

	var response map[string]any

	for _, block := range strings.Split(stream, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			var event map[string]any

			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
				continue
			}

			if event["type"] == "response.incomplete" {
				response, _ = event["response"].(map[string]any)
			}

			if event["type"] == "response.completed" {
				t.Error("a truncated response must not terminate with response.completed")
			}
		}
	}

	if response == nil {
		t.Fatalf("stream has no response.incomplete terminal event\n%s", stream)
	}

	if response["status"] != "incomplete" {
		t.Errorf("response status = %v, want incomplete", response["status"])
	}

	details, _ := response["incomplete_details"].(map[string]any)

	if details == nil {
		t.Fatal("response.incomplete carries no incomplete_details")
	}

	if details["reason"] != "max_output_tokens" {
		t.Errorf("incomplete_details.reason = %v, want max_output_tokens", details["reason"])
	}
}

// A truncated call must not claim its arguments are final: emitting
// function_call_arguments.done would tell the client the JSON is complete.
func TestStreamOmitsArgumentsDoneForTruncatedToolCall(t *testing.T) {
	stream := streamTruncatedCall(t)

	if strings.Contains(stream, "event: response.function_call_arguments.done") {
		t.Errorf("truncated call must not emit function_call_arguments.done\n%s", stream)
	}
}
