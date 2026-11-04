package anthropic

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
)

func TestStreamingReferenceRejection(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"unsupported system role"}}`))
	}))
	defer s.Close()
	h := &Harness{Client: harness.NewClient()}
	events := PostMessagesSSEAllowRejection(t, h, harness.Endpoint{Name: "reference", BaseURL: s.URL}, map[string]any{"stream": true})
	if len(events) != 1 || events[0].Event != "error" || events[0].Data["type"] != "error" {
		t.Fatalf("reference rejection lost: %+v", events)
	}
}

func TestThinkingStreamComparisonPreservesLifecycleChecks(t *testing.T) {
	var events []*harness.SSEEvent
	for _, raw := range []string{
		`{"type":"message_start"}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		// Gemini can send its signature while the text block is still open.
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"signed"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_stop"}`,
	} {
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatal(err)
		}
		events = append(events, &harness.SSEEvent{Event: data["type"].(string), Data: data})
	}
	ValidateMessageStream(t, events)
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if got := SSEEventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("event pattern = %v, want %v", got, want)
	}
	if len(events) != 9 || len(WithoutThinking(events)) != 6 {
		t.Fatal("comparison must retain the original signed events")
	}
	for name, invalid := range map[string][]*harness.SSEEvent{
		"missing message start": events[1:],
		"unfinished message":    events[:8],
		"duplicate block":       append(append([]*harness.SSEEvent{}, events[:2]...), events[1:]...),
		"delta before start":    append(append([]*harness.SSEEvent{}, events[:1]...), events[2:]...),
		"unclosed thinking":     append(append([]*harness.SSEEvent{}, events[:5]...), events[6:]...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := messageStreamError(invalid); err == nil {
				t.Fatal("accepted malformed stream")
			}
		})
	}
}
