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
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func reviewRequest(t *testing.T, c provider.Completer, stream bool) (json.RawMessage, string) {
	t.Helper()
	body := fmt.Sprintf(`{"model":"test","input":"check","stream":%t,"include":["reasoning.encrypted_content"],"reasoning":{"effort":"low","summary":"auto"}}`, stream)
	return reviewRequestBody(t, c, stream, body)
}

func reviewRequestBody(t *testing.T, c provider.Completer, stream bool, body string) (json.RawMessage, string) {
	t.Helper()
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", c)
	rec := httptest.NewRecorder()
	New(cfg).handleResponses(rec, httptest.NewRequest("POST", "/responses", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if !stream {
		return rec.Body.Bytes(), rec.Body.String()
	}
	var result json.RawMessage
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var e struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			t.Fatal(err)
		}
		if e.Type == "response.completed" || e.Type == "response.failed" || e.Type == "response.incomplete" {
			result = e.Response
		}
	}
	if result == nil {
		t.Fatalf("no terminal response: %s", rec.Body.String())
	}
	return result, rec.Body.String()
}

func TestResponsesRedactedThinkingRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			c := reasoningStatusCompleter{status: provider.CompletionStatusCompleted, content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{ID: "rs_redacted", Signature: "opaque_redacted_blob", Redacted: true}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_a", Name: "lookup", Arguments: `{}`}),
			}}
			payload, _ := reviewRequest(t, c, stream)
			var result struct {
				Output ResponsesInput `json:"output"`
			}
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			messages, err := toMessages(result.Output.Items, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range messages {
				for _, part := range m.Content {
					if r := part.Reasoning; r != nil {
						if !r.Redacted || r.Signature != "opaque_redacted_blob" {
							t.Fatalf("redacted block changed on replay: Redacted=%t Signature=%q; output=%s", r.Redacted, r.Signature, payload)
						}
						return
					}
				}
			}
			t.Fatal("redacted block disappeared")
		})
	}
}

func reviewSummaryEvents(parts []string) string {
	var b strings.Builder
	sequence := 0
	emit := func(kind string, event map[string]any) {
		event["type"] = kind
		event["sequence_number"] = sequence
		sequence++
		data, _ := json.Marshal(event)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", kind, data)
	}
	emit("response.created", map[string]any{"response": map[string]any{"id": "resp_test", "model": "gpt-test", "status": "in_progress", "output": []any{}}})
	item := map[string]any{"id": "rs1", "type": "reasoning", "summary": []any{}, "status": "in_progress"}
	emit("response.output_item.added", map[string]any{"output_index": 0, "item": item})
	summaries := []any{}
	for i, text := range parts {
		part := map[string]any{"type": "summary_text", "text": ""}
		emit("response.reasoning_summary_part.added", map[string]any{"item_id": "rs1", "output_index": 0, "summary_index": i, "part": part})
		emit("response.reasoning_summary_text.delta", map[string]any{"item_id": "rs1", "output_index": 0, "summary_index": i, "delta": text})
		emit("response.reasoning_summary_text.done", map[string]any{"item_id": "rs1", "output_index": 0, "summary_index": i, "text": text})
		part["text"] = text
		emit("response.reasoning_summary_part.done", map[string]any{"item_id": "rs1", "output_index": 0, "summary_index": i, "part": part})
		summaries = append(summaries, part)
	}
	item["summary"], item["status"], item["encrypted_content"] = summaries, "completed", "opaque_signature"
	emit("response.output_item.done", map[string]any{"output_index": 0, "item": item})
	emit("response.completed", map[string]any{"response": map[string]any{"id": "resp_test", "model": "gpt-test", "status": "completed", "output": []any{item}}})
	return b.String()
}

func TestResponsesOpenAISummaryParts(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			parts := []string{"First paragraph.", "", "Second paragraph."}
			requests := 0
			client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if requests == 2 {
					var req struct {
						Input []struct {
							Type             string
							Summary          []struct{ Text string }
							EncryptedContent string `json:"encrypted_content"`
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					var replay []string
					for _, item := range req.Input {
						if item.Type == "reasoning" {
							if item.EncryptedContent != "opaque_signature" {
								t.Errorf("signature changed: %q", item.EncryptedContent)
							}
							for _, part := range item.Summary {
								replay = append(replay, part.Text)
							}
						}
					}
					if want := []string{parts[0], parts[2]}; !reflect.DeepEqual(replay, want) {
						t.Errorf("replayed summary = %q, want %q", replay, want)
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(reviewSummaryEvents(parts))), Request: r}, nil
			})}
			c, err := openai.NewResponder("http://upstream.test", "gpt-test", openai.WithToken("test"), openai.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			payload, wire := reviewRequest(t, c, stream)
			var result struct {
				Output []struct {
					Type    string
					Summary []struct{ Text string }
				}
			}
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Output) != 1 || len(result.Output[0].Summary) != len(parts) {
				t.Fatalf("summary parts flattened: %s", payload)
			}
			for i, part := range result.Output[0].Summary {
				if part.Text != parts[i] {
					t.Fatalf("summary part %d changed: %s", i, payload)
				}
			}
			if stream {
				var added, done []int
				for _, line := range strings.Split(wire, "\n") {
					data, ok := strings.CutPrefix(line, "data: ")
					if !ok {
						continue
					}
					var e struct {
						Type  string
						Index int `json:"summary_index"`
						Part  struct{ Text string }
					}
					if err := json.Unmarshal([]byte(data), &e); err != nil {
						t.Fatal(err)
					}
					if e.Type == "response.reasoning_summary_part.added" {
						added = append(added, e.Index)
					}
					if e.Type == "response.reasoning_summary_part.done" {
						done = append(done, e.Index)
						if e.Index >= len(parts) || e.Index < 0 || e.Part.Text != parts[e.Index] {
							t.Errorf("wrong summary part: %s", data)
						}
					}
				}
				if !reflect.DeepEqual(added, []int{0, 1, 2}) || !reflect.DeepEqual(done, added) {
					t.Errorf("summary lifecycle: added=%v done=%v", added, done)
				}
			}
			var output struct{ Output json.RawMessage }
			if err := json.Unmarshal(payload, &output); err != nil {
				t.Fatal(err)
			}
			reviewRequestBody(t, c, stream, fmt.Sprintf(`{"model":"test","input":%s,"stream":%t}`, output.Output, stream))
			if requests != 2 {
				t.Errorf("upstream requests = %d, want 2", requests)
			}
		})
	}
}

func TestResponsesInterleavedArgumentsRemainOpen(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator(func(e StreamEvent) error { events = append(events, e); return nil })
	for _, call := range []provider.ToolCall{
		{ID: "a", Name: "lookup", Arguments: `{}`},
		{ID: "b", Name: "lookup", Arguments: `{`},
		{ID: "a", Arguments: " "},
		{ID: "a"},
		{ID: "b", Arguments: `}`},
		{ID: "b"},
	} {
		if err := acc.Add(provider.Completion{Message: &provider.Message{Content: []provider.Content{provider.ToolCallContent(call)}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}
	closed := map[string]bool{}
	for _, e := range events {
		if e.Type == StreamEventFunctionCallDone {
			closed[e.ToolCallID] = true
		}
		if e.Type == StreamEventFunctionCallArgumentsDelta && closed[e.ToolCallID] {
			t.Fatalf("argument delta %q emitted for call %s after output_item.done", e.Delta, e.ToolCallID)
		}
	}
	if len(closed) != 2 {
		t.Fatalf("closed calls = %v", closed)
	}
	if err := acc.Add(provider.Completion{Message: &provider.Message{Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "a", Arguments: " "})}}}); err == nil {
		t.Error("arguments after native completion must be rejected")
	}
}

func TestResponsesPreserveMixedRedactedBlocks(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			original := []provider.Reasoning{
				{Content: provider.TextParts("before"), Signature: "sig-before"},
				{Signature: "cmVkYWN0ZWQ=", Redacted: true},
				{Content: provider.TextParts("after"), Signature: "sig-after"},
			}
			contents := []provider.Content{}
			for _, r := range original {
				contents = append(contents, provider.ReasoningContent(r))
			}
			contents = append(contents, provider.ToolCallContent(provider.ToolCall{ID: "c1", Name: "lookup", Arguments: `{}`}))
			payload, _ := reviewRequest(t, reasoningStatusCompleter{status: provider.CompletionStatusCompleted, content: contents}, stream)
			var result struct{ Output ResponsesInput }
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			messages, err := toMessages(result.Output.Items, "")
			if err != nil {
				t.Fatal(err)
			}
			var replay []provider.Reasoning
			for _, m := range messages {
				for _, part := range m.Content {
					if part.Reasoning != nil {
						replay = append(replay, *part.Reasoning)
					}
				}
			}
			if len(replay) != len(original) {
				t.Fatalf("reasoning blocks merged: %+v", replay)
			}
			for i, r := range replay {
				if r.Signature != original[i].Signature || r.Redacted != original[i].Redacted || r.Text()+r.SummaryText() != original[i].Text() {
					t.Errorf("block %d changed: %+v", i, r)
				}
			}
		})
	}
}

func TestResponsesRejectMalformedRedactedEnvelope(t *testing.T) {
	_, err := toMessages([]InputItem{{Type: InputItemTypeReasoning, InputReasoning: &InputReasoning{EncryptedContent: redactedReasoningPrefix + "!invalid"}}}, "")
	if err == nil {
		t.Fatal("malformed envelope accepted")
	}
}

func TestResponsesReasoningPartsSparseAndEmpty(t *testing.T) {
	for _, emptySummary := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("empty=%t/stream=%t", emptySummary, stream), func(t *testing.T) {
				r := provider.Reasoning{ID: "rs_parts", Content: []provider.Part{{Index: 0, Text: "A"}, {Index: 2, Text: "B"}}, Summary: []provider.Part{{Index: 0, Text: "C"}, {Index: 2, Text: "D"}}}
				if emptySummary {
					r.Summary = []provider.Part{{Text: ""}}
				}
				payload, wire := reviewRequest(t, reasoningStatusCompleter{status: provider.CompletionStatusCompleted, content: []provider.Content{provider.ReasoningContent(r)}}, stream)
				var result struct {
					Output []struct {
						Content []struct{ Text string }
						Summary []struct{ Text string }
					}
				}
				if err := json.Unmarshal(payload, &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Output) != 1 {
					t.Fatalf("reasoning item lost: %s", payload)
				}
				if !emptySummary && (len(result.Output[0].Content) != 2 || result.Output[0].Content[0].Text != "A" || result.Output[0].Content[1].Text != "B") {
					t.Fatalf("reasoning content lost: %s", payload)
				}
				parts := result.Output[0].Summary
				if emptySummary {
					if len(result.Output[0].Content) != 0 || len(parts) != 2 || parts[0].Text != "A" || parts[1].Text != "B" {
						t.Fatalf("raw summary fallback lost its boundaries: %s", payload)
					}
				} else if len(parts) != 2 || parts[0].Text != "C" || parts[1].Text != "D" {
					t.Fatalf("sparse summary lost: %s", payload)
				}
				if stream {
					n := 0
					for _, line := range strings.Split(wire, "\n") {
						data, ok := strings.CutPrefix(line, "data: ")
						if !ok {
							continue
						}
						var e struct {
							Type  string
							Index int `json:"summary_index"`
							Part  struct{ Text string }
						}
						if err := json.Unmarshal([]byte(data), &e); err != nil {
							t.Fatal(err)
						}
						if e.Type != "response.reasoning_summary_part.done" {
							continue
						}
						if e.Index != n || n >= len(parts) || e.Part.Text != parts[n].Text {
							t.Fatalf("stream and snapshot have different summary parts: %s", data)
						}
						n++
					}
					if n != len(parts) {
						t.Fatalf("stream emitted %d summary parts, snapshot has %d", n, len(parts))
					}
				}
			})
		}
	}
}

func TestResponsesToolsWaitForFinalAcceptance(t *testing.T) {
	for _, refusal := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_refusal=%t", refusal), func(t *testing.T) {
			var done []StreamEvent
			argsDone := 0
			acc := NewStreamingAccumulator(func(e StreamEvent) error {
				if e.Type == StreamEventFunctionCallDone {
					done = append(done, e)
				}
				if e.Type == StreamEventFunctionCallArgumentsDone {
					argsDone++
				}
				return nil
			})
			for _, c := range []provider.Content{
				provider.ToolCallContent(provider.ToolCall{ID: "a", Name: "lookup", Arguments: `{}`}),
				provider.ToolCallContent(provider.ToolCall{ID: "b", Name: "lookup", Arguments: `{}`}),
				{MessageID: "after_tools", Text: "trailing text"},
			} {
				if err := acc.Add(provider.Completion{Message: &provider.Message{Content: []provider.Content{c}}}); err != nil {
					t.Fatal(err)
				}
			}
			if len(done) > 0 {
				t.Fatal("calls finalized before the turn's acceptance was known")
			}
			final := provider.Completion{Status: provider.CompletionStatusRefused}
			if refusal {
				final.Message = &provider.Message{Content: []provider.Content{provider.RefusalContent("declined")}}
			}
			if err := acc.Add(final); err != nil {
				t.Fatal(err)
			}
			if err := acc.Complete(); err != nil {
				t.Fatal(err)
			}
			if len(done) != 2 || argsDone != 0 {
				t.Fatalf("done=%v args_done=%d", done, argsDone)
			}
			for _, e := range done {
				if !e.Incomplete {
					t.Errorf("filtered call %s marked completed", e.ToolCallID)
				}
			}
		})
	}
}

func TestResponsesRedactedReplayRespectsSignatureRealm(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, realm := range []string{"source", "other"} {
			t.Run(fmt.Sprintf("%s/stream=%t", realm, stream), func(t *testing.T) {
				producer := signatures.ScopedTo("source", reasoningStatusCompleter{status: provider.CompletionStatusCompleted, content: []provider.Content{
					provider.ReasoningContent(provider.Reasoning{Signature: "opaque-redacted", Redacted: true}),
					provider.TextContent("Answer"),
				}})
				payload, _ := reviewRequest(t, producer, stream)
				var result struct{ Output json.RawMessage }
				if err := json.Unmarshal(payload, &result); err != nil {
					t.Fatal(err)
				}
				consumer := &capturingCompleter{}
				reviewRequestBody(t, signatures.ScopedTo(realm, consumer), stream, fmt.Sprintf(`{"model":"test","input":%s,"stream":%t}`, result.Output, stream))
				count := 0
				for _, m := range consumer.gotMessages {
					for _, c := range m.Content {
						if r := c.Reasoning; r != nil {
							count++
							if !r.Redacted || r.Signature != "opaque-redacted" {
								t.Errorf("replayed reasoning changed: %+v", r)
							}
						}
					}
				}
				want := 0
				if realm == "source" {
					want = 1
				}
				if count != want {
					t.Errorf("replayed %d redacted blocks, want %d", count, want)
				}
			})
		}
	}
}

func TestResponsesGeminiAcceptsBestEffortParallelControl(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				events := geminiSSE(
					`{"event_type":"interaction.created","interaction":{"id":"r1","model":"gemini-test","status":"in_progress"}}`,
					`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"DONE"}]}}`,
					`{"event_type":"step.stop","index":0}`,
					`{"event_type":"interaction.completed","interaction":{"id":"r1","status":"completed"}}`,
				)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events)), Request: r}, nil
			})}
			c, err := google.NewCompleter("gemini-test", google.WithToken("test"), google.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("test", c)
			body := fmt.Sprintf(`{"model":"test","input":"check","stream":%t,"parallel_tool_calls":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}]}`, stream)
			rec := httptest.NewRecorder()
			New(cfg).handleResponses(rec, httptest.NewRequest("POST", "/responses", strings.NewReader(body)))
			if rec.Code != http.StatusOK || requests != 1 || !strings.Contains(rec.Body.String(), "DONE") {
				t.Fatalf("best-effort control rejected: HTTP %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
