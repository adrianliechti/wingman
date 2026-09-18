package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestResponderPreservesEffectiveReasoningContext(t *testing.T) {
	for _, mode := range []provider.ReasoningContext{"", provider.ReasoningContextCurrentTurn, provider.ReasoningContextAllTurns} {
		for _, source := range []string{"created", "completed"} {
			t.Run(string(mode)+"/"+source, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, event := range []string{"created", "completed"} {
						response := map[string]any{"id": "resp_1", "model": "deployment-alias", "output": []any{}}
						if event == source && mode != "" {
							response["reasoning"] = map[string]any{"context": mode}
						}
						if event == "completed" {
							response["status"] = "completed"
							response["usage"] = map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
						} else {
							response["status"] = "in_progress"
						}
						data, err := json.Marshal(map[string]any{"type": "response." + event, "response": response})
						if err != nil {
							t.Error(err)
							return
						}
						fmt.Fprintf(w, "event: response.%s\ndata: %s\n\n", event, data)
					}
				}))
				defer server.Close()
				responder, err := NewResponder(server.URL, "gpt-5.6", WithToken("test-token"))
				if err != nil {
					t.Fatal(err)
				}
				var final *provider.Completion
				for completion, err := range responder.Complete(context.Background(), []provider.Message{provider.UserMessage("Hello")}, nil) {
					if err != nil {
						t.Fatal(err)
					}
					if completion.Status == provider.CompletionStatusCompleted {
						final = completion
					}
				}
				if final == nil || final.Reasoning != mode {
					t.Fatalf("final = %+v, want effective context %q", final, mode)
				}
				if final.Usage == nil || final.Usage.HasReasoningTokens() {
					t.Fatalf("invented reasoning breakdown: %+v", final.Usage)
				}
			})
		}
	}
}
