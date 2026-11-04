package openai

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestStopSequenceAcrossWireFragments(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, tc := range []struct {
			name             string
			stops, fragments []string
			want, stop       string
		}{
			{"split", []string{"END"}, []string{"safe E", "N", "D secret"}, "safe ", "END"},
			{"unicode", []string{"🚀END"}, []string{"safe 🚀", "EN", "D secret"}, "safe ", "🚀END"},
			{"partial_at_eof", []string{"END"}, []string{"safe E", "N"}, "safe EN", ""},
			{"false_prefix", []string{"END"}, []string{"safe E", "X", "N"}, "safe EXN", ""},
			{"overlap", []string{"ABAB", "BAB"}, []string{"safe A", "B", "A", "B secret"}, "safe ", "ABAB"},
			{"at_start", []string{"END"}, []string{"E", "ND secret"}, "", "END"},
		} {
			t.Run(fmt.Sprintf("responses=%v/%s", responses, tc.name), func(t *testing.T) {
				wire := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, fragment := range tc.fragments {
						var event any
						if responses {
							event = map[string]any{"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": fragment}
						} else {
							event = map[string]any{"id": "chat_1", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": fragment}}}}
						}
						raw, _ := json.Marshal(event)
						fmt.Fprintf(w, "data: %s\n\n", raw)
						http.NewResponseController(w).Flush()
					}
					if responses {
						io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]}}\n\n")
					} else {
						io.WriteString(w, "data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					}
				}))
				t.Cleanup(wire.Close)
				var backend provider.Completer
				var err error
				if responses {
					backend, err = NewResponder(wire.URL, "gpt-5.4", WithToken("test"), WithMaxRetries(0))
				} else {
					backend, err = NewCompleter(wire.URL, "gpt-5.4", WithToken("test"), WithMaxRetries(0))
				}
				if err != nil {
					t.Fatal(err)
				}
				var text strings.Builder
				var stop string
				for chunk, err := range backend.Complete(t.Context(), []provider.Message{provider.UserMessage("hello")}, &provider.CompleteOptions{Stop: tc.stops}) {
					if err != nil {
						t.Fatal(err)
					}
					if chunk.Message != nil {
						text.WriteString(chunk.Message.Text())
					}
					if chunk.StopSequence != "" {
						stop = chunk.StopSequence
					}
				}
				if text.String() != tc.want || stop != tc.stop {
					t.Fatalf("text=%q stop=%q, want %q / %q", text.String(), stop, tc.want, tc.stop)
				}
			})
		}
	}
}
