package responses

import (
	"bytes"
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

const refusalTestModel = "refusal-test-model"

// refusalCompleter streams a refusal in pieces, ending with a refused status.
type refusalCompleter struct {
	chunks []string
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
