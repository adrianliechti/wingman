package responses

import (
	"context"
	"encoding/json"
	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type phaseCompleter struct {
	t                         *testing.T
	tool, incomplete, refusal bool
}

func (c phaseCompleter) Complete(_ context.Context, _ []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options.Schema == nil || options.Schema.Strict == nil || !*options.Schema.Strict || options.Schema.Name != "project" {
			c.t.Error("strict schema was not forwarded")
		}
		emit := func(phase provider.MessagePhase, part provider.Content) bool {
			return yield(&provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Phase: phase, Content: []provider.Content{part}}}, nil)
		}
		// A valid JSON update must never get concatenated with the final JSON.
		if !emit(provider.MessagePhaseCommentary, provider.TextContent(`{"apps":[]}`)) {
			return
		}
		if c.tool && !emit("", provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "lookup", Arguments: `{}`})) {
			return
		}
		if c.refusal {
			if !emit(provider.MessagePhaseFinalAnswer, provider.RefusalContent("Cannot answer")) {
				return
			}
		} else {
			if !emit(provider.MessagePhaseFinalAnswer, provider.TextContent(`{"apps":`)) {
				return
			}
			if !emit("", provider.TextContent(`[]}`)) {
				return
			}
		}
		status := provider.CompletionStatusCompleted
		if c.incomplete {
			status = provider.CompletionStatusIncomplete
		}
		yield(&provider.Completion{Status: status}, nil)
	}
}

func TestResponsesPreserveMessagePhases(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name                      string
			tool, incomplete, refusal bool
		}{{name: "text"}, {name: "tool between phases", tool: true}, {name: "truncated final", incomplete: true}, {name: "refusal", refusal: true}} {
			t.Run(tc.name+map[bool]string{false: "/json", true: "/stream"}[stream], func(t *testing.T) {
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("phase-test", phaseCompleter{t: t, tool: tc.tool, incomplete: tc.incomplete, refusal: tc.refusal})
				body, _ := json.Marshal(map[string]any{"model": "phase-test", "input": "read the project", "stream": stream, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "project", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"apps": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"apps"}, "additionalProperties": false}}}})
				rec := httptest.NewRecorder()
				New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(string(body))))
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
				}
				var result map[string]any
				added, done := map[string]map[string]any{}, map[string]map[string]any{}
				if stream {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						var event map[string]any
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
							t.Fatal(err)
						}
						switch event["type"] {
						case "response.output_item.added", "response.output_item.done":
							item := event["item"].(map[string]any)
							if item["type"] != "message" {
								continue
							}
							id := item["id"].(string)
							if event["type"] == "response.output_item.added" {
								if added[id] != nil {
									t.Fatal("message ID reused")
								}
								added[id] = item
							} else {
								if added[id] == nil {
									t.Fatal("message completed before being added")
								}
								done[id] = item
							}
						case "response.output_text.delta":
							id := event["item_id"].(string)
							if added[id] == nil || done[id] != nil {
								t.Fatal("text arrived outside its message lifecycle")
							}
						case "response.completed", "response.incomplete":
							result = event["response"].(map[string]any)
						}
					}
				} else if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				var messages []map[string]any
				for _, value := range result["output"].([]any) {
					item := value.(map[string]any)
					if item["type"] == "message" {
						messages = append(messages, item)
					}
				}
				if len(messages) != 2 {
					t.Fatalf("got %d messages, want 2: %s", len(messages), rec.Body.String())
				}
				for i, item := range messages {
					wantPhase := []string{"commentary", "final_answer"}[i]
					if item["phase"] != wantPhase {
						t.Fatalf("phase = %v, want %s", item["phase"], wantPhase)
					}
					content := item["content"].([]any)[0].(map[string]any)
					if i == 1 && tc.refusal {
						if content["type"] != "refusal" {
							t.Fatal("lost refusal")
						}
					} else if content["text"] != `{"apps":[]}` {
						t.Fatalf("merged phases: %v", content)
					}
					wantStatus := "completed"
					if i == 1 && tc.incomplete {
						wantStatus = "incomplete"
					}
					if item["status"] != wantStatus {
						t.Fatalf("status = %v, want %s", item["status"], wantStatus)
					}
					if stream {
						id := item["id"].(string)
						if done[id] == nil || done[id]["phase"] != wantPhase {
							t.Fatalf("stream and snapshot disagree: %v", item)
						}
					}
				}
			})
		}
	}
}

type repeatedPhaseCompleter struct{}

// Recorded from the Responses API with a hosted tool: commentary, tool call,
// commentary, final answer. Phase announcements precede each item's text.
func (repeatedPhaseCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		chunks := []*provider.Completion{
			{Message: &provider.Message{Role: provider.MessageRoleAssistant, Phase: provider.MessagePhaseCommentary}},
			{Message: &provider.Message{Content: []provider.Content{provider.TextContent("Searching Zurich.")}}},
			{Message: &provider.Message{Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "search", Arguments: `{}`})}}},
			{Message: &provider.Message{Role: provider.MessageRoleAssistant, Phase: provider.MessagePhaseCommentary}},
			{Message: &provider.Message{Content: []provider.Content{provider.TextContent("Searching Geneva.")}}},
			{Message: &provider.Message{Role: provider.MessageRoleAssistant, Phase: provider.MessagePhaseFinalAnswer}},
			{Message: &provider.Message{Content: []provider.Content{provider.TextContent("Zurich: 452,421")}}},
			{Status: provider.CompletionStatusCompleted},
		}
		for _, chunk := range chunks {
			if !yield(chunk, nil) {
				return
			}
		}
	}
}

func TestResponsesKeepRepeatedPhaseItemsApart(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("phase-test", repeatedPhaseCompleter{})

			body, _ := json.Marshal(map[string]any{"model": "phase-test", "input": "populations", "stream": stream, "tools": []map[string]any{{"type": "function", "name": "search", "parameters": map[string]any{"type": "object"}}}})
			rec := httptest.NewRecorder()
			New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(string(body))))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}

			var result map[string]any
			streamed := map[string]string{}
			doneIDs := map[string]bool{}

			if stream {
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event map[string]any
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
						t.Fatal(err)
					}
					switch event["type"] {
					case "response.output_text.delta":
						streamed[event["item_id"].(string)] += event["delta"].(string)
					case "response.output_item.done":
						if item := event["item"].(map[string]any); item["type"] == "message" {
							doneIDs[item["id"].(string)] = true
						}
					case "response.completed":
						result = event["response"].(map[string]any)
					}
				}
			} else if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}

			var types []string
			var messages []map[string]any
			for _, value := range result["output"].([]any) {
				item := value.(map[string]any)
				types = append(types, item["type"].(string))
				if item["type"] == "message" {
					messages = append(messages, item)
				}
			}

			if got := strings.Join(types, ","); got != "message,function_call,message,message" {
				t.Fatalf("output order = %s", got)
			}

			want := []struct{ phase, text string }{
				{"commentary", "Searching Zurich."},
				{"commentary", "Searching Geneva."},
				{"final_answer", "Zurich: 452,421"},
			}

			seen := map[string]bool{}
			for i, item := range messages {
				id := item["id"].(string)
				text := item["content"].([]any)[0].(map[string]any)["text"]
				if item["phase"] != want[i].phase || text != want[i].text || item["status"] != "completed" {
					t.Fatalf("message %d = %v, want %+v", i, item, want[i])
				}
				if !strings.HasPrefix(id, "msg_") || seen[id] {
					t.Fatalf("message %d has id %q", i, id)
				}
				seen[id] = true
				if stream && (streamed[id] != want[i].text || !doneIDs[id]) {
					t.Fatalf("message %d (%s) streamed %q, done=%v; snapshot says %q", i, id, streamed[id], doneIDs[id], want[i].text)
				}
			}
		})
	}
}
