package openai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Codex continues a turn only when the backend reports end_turn false, so the claim must survive the adapter.
func TestResponderKeepsUpstreamEndTurn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upstream string
		want     *bool
	}{
		{"continue", `,"end_turn":false`, new(false)},
		{"ended", `,"end_turn":true`, new(true)},
		{"no claim", ``, nil},
		{"null", `,"end_turn":null`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n")
				fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"%s}}\n\n", tc.upstream)
			}))
			defer server.Close()
			responder, err := NewResponder(server.URL, "test", WithToken("test"))
			if err != nil {
				t.Fatal(err)
			}
			var acc provider.CompletionAccumulator
			for delta, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, nil) {
				if err != nil {
					t.Fatal(err)
				}
				acc.Add(*delta)
			}
			got := acc.Result().EndTurn
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("EndTurn = %v, want %v", got, tc.want)
			}
		})
	}
}
