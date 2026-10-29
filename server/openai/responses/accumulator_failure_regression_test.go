package responses

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
)

func completionParts(parts ...provider.Content) provider.Completion {
	return provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: parts}}
}

func TestFailedResponsesNeverFinalizeOpenCalls(t *testing.T) {
	for _, reported := range []bool{false, true} {
		for _, args := range []string{`{"path":"file"}`, ""} {
			t.Run(fmt.Sprintf("reported=%t/args=%q", reported, args), func(t *testing.T) {
				chunks := []provider.Completion{
					completionParts(provider.ToolCallContent(provider.ToolCall{ID: "first", Name: "write", Arguments: args})),
					completionParts(provider.ToolCallContent(provider.ToolCall{ID: "second", Name: "write", Arguments: `{}`})),
					completionParts(provider.Content{MessageID: "msg_pending", Text: "queued text"}),
				}
				c := responsesContractCompleter{chunks: chunks, err: errors.New("connection reset")}
				if reported {
					c.err = nil
					c.chunks = append(c.chunks, provider.Completion{Status: provider.CompletionStatusFailed})
				}
				payload, wire := reviewRequest(t, c, true)
				events, err := harness.ParseSSE(strings.NewReader(wire))
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range events {
					if e.Event == "response.function_call_arguments.done" || e.Event == "response.output_item.done" {
						t.Fatalf("failed stream finalized a pending item: %+v", e)
					}
				}
				var result struct {
					Status string
					Output []struct{ Type, Status, Arguments string }
				}
				if err := json.Unmarshal(payload, &result); err != nil {
					t.Fatal(err)
				}
				if result.Status != "failed" || len(result.Output) != 1 {
					t.Fatalf("wrong failed snapshot: %s", payload)
				}
				for _, item := range result.Output {
					if item.Status != "incomplete" {
						t.Fatalf("pending item became completed: %s", payload)
					}
				}
				if result.Output[0].Arguments != args {
					t.Fatalf("failed call arguments changed: %s", payload)
				}
			})
		}
	}
}

func TestHostedToolResultResumesStreamingBeforeCompletion(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	chunks := []provider.Completion{
		completionParts(provider.ToolCallContent(provider.ToolCall{ID: "search", Kind: provider.ToolKindToolSearch, Name: "tool_search", Execution: "server", Arguments: `{"query":"lookup"}`})),
		completionParts(provider.ToolResultContent(provider.ToolResult{ID: "search", Kind: provider.ToolKindToolSearch, Execution: "server"})),
		completionParts(provider.TextContent("The answer ")),
		completionParts(provider.TextContent("is ready.")),
	}
	for _, chunk := range chunks {
		if err := acc.Add(chunk); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, e := range events {
		switch e.Type {
		case StreamEventFunctionCallDone:
			if e.Incomplete {
				t.Fatal("completed hosted call is incomplete")
			}
			got = append(got, "call.done")
		case StreamEventToolSearchResult:
			got = append(got, "result")
		case StreamEventTextDelta:
			got = append(got, e.Delta)
		}
	}
	if want := []string{"call.done", "result", "The answer ", "is ready."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events before Complete = %q, want %q", got, want)
	}
	// A subsequent caller-owned tool must still wait for turn acceptance.
	if err := acc.Add(completionParts(provider.ToolCallContent(provider.ToolCall{ID: "write", Name: "write", Arguments: `{}`}))); err != nil {
		t.Fatal(err)
	}
	if err := acc.Error(errors.New("upstream disconnected")); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.ToolCallID == "write" && (e.Type == StreamEventFunctionCallDone || e.Type == StreamEventFunctionCallArgumentsDone) {
			t.Fatal("a hosted result made a later caller-owned call executable")
		}
	}
}

func TestResponsesMixedReasoningArrays(t *testing.T) {
	for _, summary := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("summary=%t/stream=%t", summary, stream), func(t *testing.T) {
				reasoning := func(content, summaries []provider.Part) provider.Completion {
					return completionParts(provider.ReasoningContent(provider.Reasoning{ID: "rs_mixed", Content: content, Summary: summaries}))
				}
				c := responsesContractCompleter{chunks: []provider.Completion{
					reasoning(provider.TextParts("raw"), nil),
					reasoning(nil, provider.TextParts("SUMMARY")),
					reasoning(nil, []provider.Part{{Index: 1, Text: "S2"}}),
					reasoning(provider.TextParts("more raw"), nil),
					{Status: provider.CompletionStatusCompleted},
				}}
				req := fmt.Sprintf(`{"model":"test","input":"check","stream":%t,"include":["reasoning.encrypted_content"]}`, stream)
				if summary {
					req = strings.TrimSuffix(req, "}") + `,"reasoning":{"summary":"auto"}}`
				}
				payload, wire := reviewRequestBody(t, c, stream, req)
				var result struct {
					Status string
					Output []struct{ Content, Summary []struct{ Text string } }
				}
				if err := json.Unmarshal(payload, &result); err != nil {
					t.Fatal(err)
				}
				if result.Status != "completed" || len(result.Output) != 1 {
					t.Fatalf("mixed reasoning failed: %s", payload)
				}
				item := result.Output[0]
				if len(item.Content) != 1 || item.Content[0].Text != "rawmore raw" || len(item.Summary) != 2 || item.Summary[0].Text != "SUMMARY" || item.Summary[1].Text != "S2" {
					t.Fatalf("independent reasoning arrays changed: %s", payload)
				}
				if stream {
					events, err := harness.ParseSSE(strings.NewReader(wire))
					if err != nil {
						t.Fatal(err)
					}
					var raw string
					summaries := map[int]string{}
					for _, e := range events {
						switch e.Event {
						case "response.reasoning_text.delta":
							raw += e.Data["delta"].(string)
						case "response.reasoning_summary_text.delta":
							summaries[int(e.Data["summary_index"].(float64))] += e.Data["delta"].(string)
						}
					}
					if raw != "rawmore raw" || !reflect.DeepEqual(summaries, map[int]string{0: "SUMMARY", 1: "S2"}) {
						t.Fatalf("streamed reasoning changed: raw=%q summaries=%v", raw, summaries)
					}
				}
			})
		}
	}
}

func TestRefusalWithToolsIsIncompleteInHTTPAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			c := responsesContractCompleter{chunks: []provider.Completion{
				completionParts(provider.ToolCallContent(provider.ToolCall{ID: "write", Name: "write"})),
				completionParts(provider.RefusalContent("Cannot perform that action.")),
				{Status: provider.CompletionStatusRefused},
			}}
			payload, wire := reviewRequest(t, c, stream)
			var result struct {
				Status            string
				IncompleteDetails struct{ Reason string } `json:"incomplete_details"`
				Output            []struct{ Type, Status, Arguments string }
			}
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != "incomplete" || result.IncompleteDetails.Reason != "content_filter" || len(result.Output) != 2 || result.Output[0].Status != "incomplete" || result.Output[0].Arguments != "" {
				t.Fatalf("refused tool became executable: %s", payload)
			}
			if stream && strings.Contains(wire, "response.function_call_arguments.done") {
				t.Fatalf("refused stream emitted arguments.done: %s", wire)
			}
		})
	}
}

func TestOutOfOrderReasoningPartsMatchHTTPAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var chunks []provider.Completion
			for _, part := range []provider.Part{{Index: 2, Text: "C"}, {Index: 0, Text: "A"}, {Index: 2, Text: "c"}, {Index: 1, Text: "B"}, {Index: 0, Text: "a"}} {
				chunks = append(chunks, completionParts(provider.ReasoningContent(provider.Reasoning{ID: "rs_interleaved", Summary: []provider.Part{part}})))
			}
			chunks = append(chunks, provider.Completion{Status: provider.CompletionStatusCompleted})
			payload, wire := reviewRequest(t, responsesContractCompleter{chunks: chunks}, stream)
			var result struct {
				Status string
				Output []struct{ Summary []struct{ Text string } }
			}
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != "completed" || len(result.Output) != 1 || len(result.Output[0].Summary) != 3 {
				t.Fatalf("failed interleaved reasoning: %s", payload)
			}
			want := []string{"Aa", "B", "Cc"}
			for i, p := range result.Output[0].Summary {
				if p.Text != want[i] {
					t.Fatalf("part %d=%q", i, p.Text)
				}
			}
			if stream {
				events, err := harness.ParseSSE(strings.NewReader(wire))
				if err != nil {
					t.Fatal(err)
				}
				parts := make([]string, 3)
				open := map[int]bool{}
				for _, e := range events {
					index, _ := e.Data["summary_index"].(float64)
					i := int(index)
					switch e.Event {
					case "response.reasoning_summary_part.added":
						open[i] = true
					case "response.reasoning_summary_text.delta":
						if !open[i] {
							t.Fatal("delta outside open part")
						}
						parts[i] += e.Data["delta"].(string)
					case "response.reasoning_summary_part.done":
						delete(open, i)
					}
				}
				if len(open) != 0 || !reflect.DeepEqual(parts, want) {
					t.Fatalf("streamed parts=%q open=%v", parts, open)
				}
			}
		})
	}
}
