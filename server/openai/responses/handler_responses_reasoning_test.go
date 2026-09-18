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

type reasoningStatusCompleter struct {
	content          []provider.Content
	status           provider.CompletionStatus
	usage            *provider.Usage
	reasoningContext provider.ReasoningContext
}

func (c reasoningStatusCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if !yield(&provider.Completion{Reasoning: c.reasoningContext, Usage: c.usage}, nil) {
			return
		}
		for _, content := range c.content {
			if !yield(&provider.Completion{Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{content},
			}}, nil) {
				return
			}
		}
		yield(&provider.Completion{Status: c.status}, nil)
	}
}

func TestResponsesPreserveReasoningMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		context provider.ReasoningContext
		tokens  *int
	}{
		{"unavailable", "", nil},
		{"measured zero", provider.ReasoningContextCurrentTurn, new(0)},
		{"measured reasoning", provider.ReasoningContextAllTurns, new(3)},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/json", true: "/stream"}[stream], func(t *testing.T) {
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("gpt-5.6", reasoningStatusCompleter{
					content:          []provider.Content{provider.TextContent("Answer")},
					status:           provider.CompletionStatusCompleted,
					reasoningContext: tc.context,
					usage:            &provider.Usage{InputTokens: 10, OutputTokens: 5, ReasoningTokens: tc.tokens},
				})
				body, err := json.Marshal(map[string]any{
					"model": "gpt-5.6", "input": "Think", "stream": stream,
					"reasoning": map[string]any{"context": "all_turns"},
				})
				if err != nil {
					t.Fatal(err)
				}
				rec := httptest.NewRecorder()
				New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body)))
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
				}
				var result *Response
				if stream {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						data, ok := strings.CutPrefix(line, "data: ")
						if !ok {
							continue
						}
						var event struct {
							Type     string    `json:"type"`
							Response *Response `json:"response"`
						}
						if err := json.Unmarshal([]byte(data), &event); err != nil {
							t.Fatal(err)
						}
						if event.Type == "response.completed" {
							result = event.Response
						}
					}
				} else if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result == nil || result.Usage == nil || result.Reasoning == nil {
					t.Fatalf("response = %+v", result)
				}
				if tc.context == "" {
					if result.Reasoning.Context != nil {
						t.Fatalf("invented context: %q", *result.Reasoning.Context)
					}
				} else if result.Reasoning.Context == nil || *result.Reasoning.Context != string(tc.context) {
					t.Fatalf("reasoning = %+v, want context %q", result.Reasoning, tc.context)
				}
				details := result.Usage.OutputTokensDetails
				if tc.tokens != nil {
					if details == nil || details.ReasoningTokens != *tc.tokens {
						t.Fatalf("details = %+v", details)
					}
				} else if details != nil {
					t.Fatalf("invented reasoning tokens: %+v", details)
				}
			})
		}
	}
}

func TestResponsesPreserveReasoningStatus(t *testing.T) {
	reasoning := provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: "First summary"})
	secondReasoning := provider.ReasoningContent(provider.Reasoning{ID: "rs_2", Summary: "Second summary"})

	cases := []struct {
		name    string
		content []provider.Content
		status  provider.CompletionStatus
		want    map[string]string
	}{
		{
			name:    "completed reasoning",
			content: []provider.Content{reasoning},
			status:  provider.CompletionStatusCompleted,
			want:    map[string]string{"rs_1": "completed"},
		},
		{
			name:    "truncated reasoning",
			content: []provider.Content{reasoning},
			status:  provider.CompletionStatusIncomplete,
			want:    map[string]string{"rs_1": "incomplete"},
		},
		{
			name:    "completed reasoning before truncated text",
			content: []provider.Content{reasoning, provider.TextContent("Partial answer")},
			status:  provider.CompletionStatusIncomplete,
			want:    map[string]string{"rs_1": "completed"},
		},
		{
			name:    "only last reasoning truncated",
			content: []provider.Content{reasoning, secondReasoning},
			status:  provider.CompletionStatusIncomplete,
			want:    map[string]string{"rs_1": "completed", "rs_2": "incomplete"},
		},
	}

	type outputItem struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
	}
	type response struct {
		Status string       `json:"status"`
		Output []outputItem `json:"output"`
	}

	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/json", true: "/stream"}[stream], func(t *testing.T) {
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("reasoning-test", reasoningStatusCompleter{content: tc.content, status: tc.status})
				body, err := json.Marshal(map[string]any{
					"model": "reasoning-test", "input": "Think about this", "stream": stream,
					"reasoning": map[string]any{"summary": "auto"},
				})
				if err != nil {
					t.Fatal(err)
				}
				rec := httptest.NewRecorder()
				New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body)))
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
				}

				var result *response
				added := make(map[string]string)
				done := make(map[string]string)
				summaryDone := make(map[string]json.RawMessage)
				if stream {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						data, ok := strings.CutPrefix(line, "data: ")
						if !ok {
							continue
						}
						var event struct {
							Type     string          `json:"type"`
							ItemID   string          `json:"item_id"`
							Status   json.RawMessage `json:"status"`
							Item     outputItem      `json:"item"`
							Response *response       `json:"response"`
						}
						if err := json.Unmarshal([]byte(data), &event); err != nil {
							t.Fatalf("unmarshal event: %v", err)
						}
						switch event.Type {
						case "response.output_item.added":
							if event.Item.Type == "reasoning" {
								added[event.Item.ID] = event.Item.Status
							}
						case "response.output_item.done":
							if event.Item.Type == "reasoning" {
								done[event.Item.ID] = event.Item.Status
							}
						case "response.reasoning_summary_part.done":
							summaryDone[event.ItemID] = event.Status
						case "response.completed", "response.incomplete":
							result = event.Response
						}
					}
				} else if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatalf("unmarshal response: %v", err)
				}

				if result == nil || result.Status != string(tc.status) {
					t.Fatalf("response = %+v, want status %s", result, tc.status)
				}
				reasoningCount := 0
				for _, item := range result.Output {
					if item.Type != "reasoning" {
						continue
					}
					reasoningCount++
					want, ok := tc.want[item.ID]
					if !ok || item.Status != want {
						t.Errorf("reasoning %s status = %q, want %q", item.ID, item.Status, want)
					}
				}
				if reasoningCount != len(tc.want) {
					t.Errorf("reasoning count = %d, want %d", reasoningCount, len(tc.want))
				}
				if stream {
					for id, want := range tc.want {
						if added[id] != "in_progress" || done[id] != want {
							t.Errorf("reasoning %s streaming status = %q -> %q, want in_progress -> %s", id, added[id], done[id], want)
						}
						status, ok := summaryDone[id]
						if !ok {
							t.Errorf("missing reasoning_summary_part.done for %s", id)
						} else if want == "incomplete" {
							if string(status) != `"incomplete"` {
								t.Errorf("summary %s status = %s, want incomplete", id, status)
							}
						} else if len(status) != 0 {
							t.Errorf("completed summary %s must omit status, got %s", id, status)
						}
					}
				}
			})
		}
	}
}
