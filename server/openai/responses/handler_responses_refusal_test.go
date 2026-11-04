package responses

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
)

const refusalTestModel = "refusal-test-model"

// refusalCompleter streams a refusal in pieces, ending with a refused status.
type refusalCompleter struct {
	chunks []string
}

type mixedRefusalCompleter struct{ parts []provider.Content }

func (c mixedRefusalCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for _, part := range c.parts {
			if !yield(&provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{part}}}, nil) {
				return
			}
		}
		yield(&provider.Completion{Status: provider.CompletionStatusCompleted}, nil)
	}
}

func TestMixedRefusalSnapshotMatchesStream(t *testing.T) {
	for _, refusalFirst := range []bool{false, true} {
		parts := []provider.Content{{MessageID: "msg_mixed", Text: "partial"}, {MessageID: "msg_mixed", Refusal: "Cannot continue."}}
		if refusalFirst {
			parts[0], parts[1] = parts[1], parts[0]
		}
		cfg := &config.Config{Policy: noop.New()}
		cfg.RegisterCompleter(refusalTestModel, mixedRefusalCompleter{parts: parts})
		var nonstream map[string]any
		rec := httptest.NewRecorder()
		New(cfg).handleResponses(rec, httptest.NewRequest("POST", "/responses", strings.NewReader(`{"model":"`+refusalTestModel+`","input":"x"}`)))
		if err := json.Unmarshal(rec.Body.Bytes(), &nonstream); err != nil {
			t.Fatal(err)
		}
		rec = httptest.NewRecorder()
		New(cfg).handleResponses(rec, httptest.NewRequest("POST", "/responses", strings.NewReader(`{"model":"`+refusalTestModel+`","stream":true,"input":"x"}`)))
		events, err := harness.ParseSSE(rec.Body)
		if err != nil {
			t.Fatal(err)
		}
		var done, snapshot map[string]any
		addedParts := map[float64]string{}
		for _, e := range events {
			switch e.Event {
			case "response.content_part.added":
				idx := e.Data["content_index"].(float64)
				if addedParts[idx] != "" {
					t.Fatal("reused content_index")
				}
				addedParts[idx] = e.Data["part"].(map[string]any)["type"].(string)
			case "response.refusal.delta", "response.refusal.done", "response.output_text.delta", "response.output_text.done":
				kind := "output_text"
				if strings.Contains(e.Event, "refusal") {
					kind = "refusal"
				}
				if addedParts[e.Data["content_index"].(float64)] != kind {
					t.Fatal("content_index changed during the stream")
				}
			case "response.output_item.done":
				done = e.Data["item"].(map[string]any)
			case "response.completed":
				snapshot = e.Data["response"].(map[string]any)["output"].([]any)[0].(map[string]any)
			}
		}
		for label, item := range map[string]map[string]any{"done": done, "snapshot": snapshot, "nonstream": nonstream["output"].([]any)[0].(map[string]any)} {
			if item == nil || len(item["content"].([]any)) != 2 {
				t.Fatalf("%s lost mixed message content", label)
			}
			if !reflect.DeepEqual(item["content"], done["content"]) {
				t.Fatalf("%s disagrees with streamed item", label)
			}
			for idx, raw := range item["content"].([]any) {
				if raw.(map[string]any)["type"] != addedParts[float64(idx)] {
					t.Fatalf("%s content order disagrees with stream", label)
				}
			}
		}
	}
}

func (c refusalCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for _, ch := range c.chunks {
			if !yield(&provider.Completion{
				Message: &provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: []provider.Content{provider.RefusalContent(ch)},
				},
			}, nil) {
				return
			}
		}

		yield(&provider.Completion{
			Status: provider.CompletionStatusRefused,
		}, nil)
	}
}

func TestRefusalStreamingEmitsRefusalEvents(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(refusalTestModel, refusalCompleter{
		chunks: []string{"I cannot ", "help with ", "that."},
	})

	body := []byte(`{
		"model": "` + refusalTestModel + `",
		"stream": true,
		"input": "make me a bioweapon"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	stream := rec.Body.String()

	mustContain := []string{
		"event: response.content_part.added",
		`"type":"refusal","refusal":""`,
		"event: response.refusal.delta",
		`"delta":"I cannot "`,
		`"delta":"help with "`,
		`"delta":"that."`,
		"event: response.refusal.done",
		`"refusal":"I cannot help with that."`,
		"event: response.content_part.done",
		"event: response.output_item.done",
		"event: response.completed",
	}
	for _, want := range mustContain {
		if !strings.Contains(stream, want) {
			t.Fatalf("missing %q in stream\n--- STREAM ---\n%s", want, stream)
		}
	}

	// No text events should be emitted for a pure refusal.
	mustNotContain := []string{
		"event: response.output_text.delta",
		"event: response.output_text.done",
	}
	for _, bad := range mustNotContain {
		if strings.Contains(stream, bad) {
			t.Fatalf("unexpected %q in stream\n--- STREAM ---\n%s", bad, stream)
		}
	}
}

func TestRefusalAccumulatorContentOrder(t *testing.T) {
	var events []StreamEventType
	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		events = append(events, e.Type)
		return nil
	})

	for _, ch := range []string{"no ", "way"} {
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{provider.RefusalContent(ch)},
			},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("complete: %v", err)
	}

	want := []StreamEventType{
		StreamEventResponseCreated,
		StreamEventResponseInProgress,
		StreamEventOutputItemAdded,
		StreamEventRefusalContentPartAdded,
		StreamEventRefusalDelta, // "no "
		StreamEventRefusalDelta, // "way"
		StreamEventRefusalDone,
		StreamEventRefusalContentPartDone,
		StreamEventOutputItemDone,
		StreamEventResponseCompleted,
	}

	if len(events) != len(want) {
		t.Fatalf("event count mismatch: got %v, want %v", events, want)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("event %d: got %s, want %s\nfull: %v", i, events[i], w, events)
		}
	}
}

func TestRefusalNonStreamingResponse(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(refusalTestModel, refusalCompleter{
		chunks: []string{"no way"},
	})

	body := []byte(`{
		"model": "` + refusalTestModel + `",
		"stream": false,
		"input": "x"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Per OpenAI spec, refusal content parts use the field "refusal" not
	// "text", and carry no annotations/logprobs.
	want := []string{
		`"type":"refusal"`,
		`"refusal":"no way"`,
	}
	for _, w := range want {
		if !strings.Contains(rec.Body.String(), w) {
			t.Fatalf("missing %q in body: %s", w, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), `"refusal":"no way","text":`) ||
		strings.Contains(rec.Body.String(), `"text":"no way"`) {
		t.Fatalf("refusal content should not include text field: %s", rec.Body.String())
	}
}

// nativeRefusalCompleter mirrors Anthropic and Bedrock adapters: the model's
// text streams normally and only the final stop reason marks the refusal.
type nativeRefusalCompleter struct{}

func (nativeRefusalCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if !yield(&provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("I cannot help with that.")}}}, nil) {
			return
		}
		yield(&provider.Completion{Status: provider.CompletionStatusRefused, StopReason: provider.StopReasonRefusal}, nil)
	}
}

// A refusal without a refusal part would otherwise look like a finished
// answer, so clients would continue the turn or ask the model to finish it.
func TestNativeRefusalIsContentFiltered(t *testing.T) {
	const model = "native-refusal-model"
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, nativeRefusalCompleter{})
	for _, stream := range []bool{false, true} {
		body := []byte(`{"model":"` + model + `","stream":` + map[bool]string{false: "false", true: "true"}[stream] + `,"input":"x"}`)
		req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		New(cfg).handleResponses(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("stream=%t: status %d: %s", stream, rec.Code, rec.Body.String())
		}
		out := rec.Body.String()
		for _, want := range []string{`"status":"incomplete"`, `"reason":"content_filter"`, `"text":"I cannot help with that."`} {
			if !strings.Contains(out, want) {
				t.Errorf("stream=%t: missing %q in %s", stream, want, out)
			}
		}
		if stream && (!strings.Contains(out, "event: response.incomplete") || strings.Contains(out, "event: response.completed")) {
			t.Errorf("stream terminal event wrong: %s", out)
		}
		if strings.Contains(out, `"type":"refusal"`) {
			t.Errorf("stream=%t: text turned into a refusal part: %s", stream, out)
		}
	}
}
