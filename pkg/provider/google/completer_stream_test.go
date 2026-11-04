package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func completeAll(t *testing.T, handler http.HandlerFunc, options *provider.CompleteOptions) ([]*provider.Completion, error) {
	t.Helper()
	c, err := NewCompleter("gemini-3.8-flash", WithToken("test-token"), WithClient(newTestClient(t, handler)))
	if err != nil {
		t.Fatal(err)
	}
	var deltas []*provider.Completion
	for delta, err := range c.Complete(context.Background(), []provider.Message{provider.UserMessage("hi")}, options) {
		if err != nil {
			return deltas, err
		}
		deltas = append(deltas, delta)
	}
	return deltas, nil
}

func sseEvent(data string) string { return "data: " + data + "\n\n" }

func TestThoughtSummaryPartsSurviveStreamingAndReplay(t *testing.T) {
	deltas, err := completeAll(t, interactionEvents(
		`{"event_type":"interaction.created","interaction":{"id":"i_parts","model":"gemini-test","status":"in_progress"}}`,
		`{"event_type":"step.start","index":0,"step":{"type":"thought","signature":"sig","summary":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":" continued"}}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"interaction.completed","interaction":{"id":"i_parts","status":"completed"}}`,
	), &provider.CompleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var acc provider.CompletionAccumulator
	for _, delta := range deltas {
		acc.Add(*delta)
	}
	result := acc.Result()
	r := result.Message.Content[0].Reasoning
	if len(r.Summary) != 2 || r.Summary[0].Text != "first" || r.Summary[1].Text != "second continued" {
		t.Fatalf("summary parts changed: %+v", r)
	}
	req, err := convertInteraction("gemini-test", []provider.Message{*result.Message}, &provider.CompleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	thought := req.Input.ArrayOfStep[0].ThoughtStep
	if thought == nil || len(thought.Summary) != 2 || thought.Summary[0].TextContent.Text != "first" || thought.Summary[1].TextContent.Text != "second continued" {
		t.Fatalf("replayed summary changed: %+v", thought)
	}
}

func TestEmptySummaryDoesNotHideReasoningContentOnReplay(t *testing.T) {
	message := provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("A", "B"), Summary: []provider.Part{{Text: ""}}, Signature: "sig"}),
	}}
	req, err := convertInteraction("gemini-test", []provider.Message{message}, &provider.CompleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	thought := req.Input.ArrayOfStep[0].ThoughtStep
	if thought == nil || len(thought.Summary) != 2 || thought.Summary[0].TextContent.Text != "A" || thought.Summary[1].TextContent.Text != "B" {
		t.Fatalf("placeholder summary hid raw thinking: %+v", thought)
	}
	if message.Content[0].Reasoning.Summary[0].Text != "" {
		t.Fatal("replay mutated the input message")
	}
}

func interactionEvents(events ...string) http.HandlerFunc {
	chunks := make([]string, len(events))
	for i, event := range events {
		chunks[i] = sseEvent(event)
	}
	return serveSSE(chunks...)
}

func accumulated(deltas []*provider.Completion) *provider.Completion {
	var result provider.CompletionAccumulator
	for _, delta := range deltas {
		result.Add(*delta)
	}
	return result.Result()
}

const createdEvent = `{"event_type":"interaction.created","interaction":{"id":"r1","model":"gemini-3.8-flash","status":"in_progress"}}`
const completedEvent = `{"event_type":"interaction.completed","interaction":{"id":"r1","status":"completed"}}`

func TestComplete_InteractionsTextReasoningAndUsage(t *testing.T) {
	handler := interactionEvents(
		createdEvent,
		`{"event_type":"step.start","index":0,"step":{"type":"thought"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"Let me think."}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"Ev8BAA=="}}`,
		`{"event_type":"step.stop","index":0,"step_usage":{"total_output_tokens":999}}`,
		`{"event_type":"step.start","index":1,"step":{"type":"model_output","content":[]}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text","text":"Hello "},"metadata":{"total_usage":{"total_input_tokens":100,"total_output_tokens":1,"total_thought_tokens":6,"total_cached_tokens":40}}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text","text":"world"}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"interaction.completed","interaction":{"id":"r1","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"Hello world"}]}],"usage":{"total_input_tokens":100,"total_output_tokens":14,"total_thought_tokens":6,"total_cached_tokens":40}}}`,
	)
	deltas, err := completeAll(t, handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := accumulated(deltas)
	if result.ID != "r1" || result.Model != "gemini-3.8-flash" || result.Message.Text() != "Hello world" || result.StopReason != provider.StopReasonEndTurn || result.Status != provider.CompletionStatusCompleted {
		t.Fatalf("unexpected completion: %+v / %+v", result, result.Message)
	}
	if result.Usage.InputTokens != 100 || result.Usage.OutputTokens != 20 || *result.Usage.ReasoningTokens != 6 || result.Usage.CacheReadInputTokens != 40 {
		t.Fatalf("incorrect inclusive/cumulative usage: %+v", result.Usage)
	}
	reasoning := result.Message.Content[0].Reasoning
	if reasoning == nil || reasoning.SummaryText() != "Let me think." || reasoning.Signature != "Ev8BAA==" {
		t.Fatalf("lost thought: %+v", result.Message.Content)
	}
	steps, err := convertMessages([]provider.Message{*result.Message})
	if err != nil || len(steps) != 2 || steps[0].ThoughtStep == nil || *steps[0].ThoughtStep.Signature != "Ev8BAA==" {
		t.Fatalf("thought does not replay: %+v, %v", steps, err)
	}
}

func TestComplete_ParallelToolsAndReplay(t *testing.T) {
	handler := interactionEvents(
		createdEvent,
		`{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"c1","name":"weather_lookup","arguments":{}}}`,
		`{"event_type":"step.start","index":1,"step":{"type":"function_call","id":"c2","name":"clock","arguments":{}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":"{\"city\":"}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":"\"Zurich\"}"}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"interaction.status_update","interaction_id":"r1","status":"requires_action"}`,
		`{"event_type":"interaction.completed","interaction":{"id":"r1","status":"requires_action","usage":{"total_input_tokens":10,"total_output_tokens":8,"total_thought_tokens":0}}}`,
	)
	options := &provider.CompleteOptions{Tools: []provider.Tool{{Name: "weather", Tools: []provider.Tool{{Name: "lookup"}}}, {Name: "clock"}}}
	deltas, err := completeAll(t, handler, options)
	if err != nil {
		t.Fatal(err)
	}
	result := accumulated(deltas)
	calls := result.Message.ToolCalls()
	if len(calls) != 2 || calls[0].ID != "c1" || calls[1].Arguments != "{}" || calls[0].Arguments != `{"city":"Zurich"}` || calls[0].Name != "lookup" || calls[0].Namespace != "weather" {
		t.Fatalf("parallel arguments/names lost: %+v", calls)
	}
	if result.StopReason != provider.StopReasonToolUse || result.Usage.ReasoningTokens == nil || *result.Usage.ReasoningTokens != 0 {
		t.Fatalf("unexpected boundary/usage: %+v", result)
	}
	messages := []provider.Message{*result.Message, provider.ToolMessage("c1", `{"temperature":12}`)}
	steps, err := convertMessages(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 || value(steps[0].ThoughtStep.Signature) != unsignedThoughtSignature || steps[1].FunctionCallStep.Name != "weather_lookup" || value(steps[3].FunctionResultStep.Name) != "weather_lookup" || steps[3].FunctionResultStep.CallID != "c1" {
		t.Fatalf("tool replay lost names/IDs: %+v", steps)
	}
}

func TestComplete_StatusAndRefusal(t *testing.T) {
	for _, test := range []struct {
		name, status, step string
		wantStatus         provider.CompletionStatus
		wantStop           provider.StopReason
	}{
		{"complete", "completed", "", provider.CompletionStatusCompleted, provider.StopReasonEndTurn},
		{"limit", "incomplete", "", provider.CompletionStatusIncomplete, provider.StopReasonMaxTokens},
		{"failed", "failed", "", provider.CompletionStatusFailed, ""},
		{"cancelled", "cancelled", "", provider.CompletionStatusFailed, ""},
		{"safety", "failed", `{"event_type":"step.start","index":0,"step":{"type":"model_output","error":{"code":7,"message":"Blocked by safety","details":[{"reason":"PROHIBITED_CONTENT"}]}}}`, provider.CompletionStatusRefused, provider.StopReasonRefusal},
		{"output_limit", "incomplete", `{"event_type":"step.start","index":0,"step":{"type":"model_output","error":{"code":11,"message":"Output token limit"}}}`, provider.CompletionStatusIncomplete, provider.StopReasonMaxTokens},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []string{createdEvent}
			if test.step != "" {
				events = append(events, test.step, `{"event_type":"step.stop","index":0}`)
			}
			events = append(events, `{"event_type":"interaction.completed","interaction":{"id":"r1","status":"`+test.status+`"}}`)
			deltas, err := completeAll(t, interactionEvents(events...), nil)
			if err != nil {
				t.Fatal(err)
			}
			result := accumulated(deltas)
			if result.Status != test.wantStatus || result.StopReason != test.wantStop {
				t.Fatalf("unexpected boundary: %+v", result)
			}
			if test.name == "safety" && result.StopDetails.Category != "PROHIBITED_CONTENT" {
				t.Fatalf("missing refusal details: %+v", result.StopDetails)
			}
		})
	}
}

func TestComplete_IncompleteToolDoesNotBecomeStreamError(t *testing.T) {
	deltas, err := completeAll(t, interactionEvents(createdEvent,
		`{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"c1","name":"lookup","arguments":{}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":"{\"query\":\"partial"}}`,
		`{"event_type":"interaction.completed","interaction":{"id":"r1","status":"incomplete","usage":{"total_input_tokens":10,"total_output_tokens":8}}}`,
	), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := accumulated(deltas)
	if result.Status != provider.CompletionStatusIncomplete || result.StopReason != provider.StopReasonMaxTokens || result.Usage.OutputTokens != 8 {
		t.Fatalf("expected incomplete boundary and usage: %+v", result)
	}
}

func TestComplete_Media(t *testing.T) {
	deltas, err := completeAll(t, interactionEvents(
		createdEvent,
		`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"image","mime_type":"image/png"}]}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"image","data":"aW1hZ2U="}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"audio","mime_type":"audio/wav","data":"YXVkaW8="}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"document","mime_type":"application/pdf","uri":"https://example.com/file.pdf"}}`,
		`{"event_type":"step.stop","index":0}`, completedEvent,
	), nil)
	if err != nil {
		t.Fatal(err)
	}
	var files []*provider.File
	for _, delta := range deltas {
		for _, part := range delta.Message.Content {
			if part.File != nil {
				files = append(files, part.File)
			}
		}
	}
	if len(files) != 3 || files[0].ContentType != "image/png" || string(files[0].Content) != "image" || string(files[1].Content) != "audio" || string(files[2].Content) != "https://example.com/file.pdf" {
		t.Fatalf("incorrect media: %+v", files)
	}
	replay, err := fileContent(files[2])
	if err != nil || replay.DocumentContent.Data != nil || value(replay.DocumentContent.URI) != "https://example.com/file.pdf" {
		t.Fatalf("URI media does not replay: %+v / %v", replay, err)
	}
}

func TestComplete_DisabledThinkingUsesLowWithoutRetry(t *testing.T) {
	calls := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			GenerationConfig struct {
				ThinkingLevel     string `json:"thinking_level"`
				ThinkingSummaries string `json:"thinking_summaries"`
			} `json:"generation_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.GenerationConfig.ThinkingLevel != "low" || body.GenerationConfig.ThinkingSummaries != "none" {
			t.Errorf("incorrect thinking config: %+v", body)
		}
		interactionEvents(createdEvent, completedEvent)(w, r)
	}
	options := &provider.CompleteOptions{ReasoningOptions: &provider.ReasoningOptions{Type: provider.ReasoningTypeDisabled}}
	deltas, err := completeAll(t, handler, options)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || accumulated(deltas).StopReason != provider.StopReasonEndTurn {
		t.Fatalf("expected one successful request, got %d / %+v", calls, deltas)
	}
	if options.ReasoningOptions.Type != provider.ReasoningTypeDisabled {
		t.Fatal("caller options mutated")
	}
}

func TestComplete_HTTPAndStreamErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		code    int
		message string
	}{
		{"gateway_auth", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"Invalid credentials","details":[{"reason":"API_KEY_INVALID"}]}}`)
		}, 401, "Invalid credentials"},
		{"interactions_auth", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"code":"UNAUTHENTICATED","message":"Invalid token"}}`)
		}, 401, "Invalid token"},
		{"gateway_array", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			io.WriteString(w, `[{"error":{"code":403,"status":"PERMISSION_DENIED","message":"This API is blocked for the key."}}]`)
		}, 403, "This API is blocked for the key."},
		{"gateway_sse", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(400)
			io.WriteString(w, sseEvent(`{"event_type":"error","error":{"code":"invalid_request","message":"Multimodal function responses are not supported for this model."}}`))
		}, 400, "Multimodal function responses are not supported for this model."},
		{"stream_error", interactionEvents(createdEvent, `{"event_type":"error","error":{"code":"INTERNAL","message":"Generation failed"}}`), 502, "Generation failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := completeAll(t, test.handler, nil)
			var apierr *provider.ProviderError
			if !errors.As(err, &apierr) || apierr.Code != test.code || apierr.Message != test.message {
				t.Fatalf("error not normalized: %#v", err)
			}
		})
	}
}

func TestComplete_TruncatedAndMalformedStreams(t *testing.T) {
	for _, test := range []struct {
		name          string
		events        []string
		unexpectedEOF bool
	}{
		{"truncated", []string{createdEvent, `{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`, `{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"partial"}}`}, true},
		{"invalid_args", []string{createdEvent, `{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"c1","name":"tool","arguments":{}}}`, `{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":"{bad"}}`, `{"event_type":"step.stop","index":0}`}, false},
		{"unknown_event", []string{createdEvent, `{"event_type":"new.unsupported"}`}, false},
		{"unfinished_tool", []string{createdEvent, `{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"c1","name":"tool","arguments":{}}}`, completedEvent}, false},
		{"unfinished_tool_status", []string{createdEvent, `{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"c1","name":"tool","arguments":{}}}`, `{"event_type":"interaction.status_update","interaction_id":"r1","status":"requires_action"}`}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := completeAll(t, interactionEvents(test.events...), nil)
			if err == nil || (test.unexpectedEOF && !errors.Is(err, io.ErrUnexpectedEOF)) {
				t.Fatalf("expected stream error, got %v", err)
			}
		})
	}
}

type interactionTransport func(*http.Request) (*http.Response, error)

func (f interactionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type interactionBody struct {
	io.Reader
	closed bool
}

func (b *interactionBody) Close() error { b.closed = true; return nil }

func TestComplete_ClosesStreamOnStopAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "consumer_stop", true: "cancelled"}[cancelled], func(t *testing.T) {
			body := &interactionBody{Reader: strings.NewReader(sseEvent(createdEvent) + sseEvent(completedEvent))}
			client := &http.Client{Transport: interactionTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body, Request: r}, nil
			})}
			c, err := NewCompleter("gemini-3.8-flash", WithToken("test-token"), WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var streamErr error
			for _, err := range c.Complete(ctx, []provider.Message{provider.UserMessage("hi")}, nil) {
				if err != nil {
					streamErr = err
					break
				}
				if !cancelled {
					break
				}
				cancel()
			}
			if !body.closed {
				t.Fatal("response body leaked")
			}
			if cancelled && !errors.Is(streamErr, context.Canceled) {
				t.Fatalf("cancellation lost: %v", streamErr)
			}
		})
	}
}

func TestComplete_JSONResponse(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r1","model":"gemini-3.8-flash","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"hello"}]}],"usage":{"total_input_tokens":1,"total_output_tokens":2,"total_thought_tokens":0}}`)
	}
	deltas, err := completeAll(t, handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result := accumulated(deltas); result.Message.Text() != "hello" || result.Status != provider.CompletionStatusCompleted {
		t.Fatalf("unexpected response: %+v", result)
	}
}
