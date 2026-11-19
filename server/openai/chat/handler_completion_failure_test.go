package chat

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type cancelledChatCompleter struct{ partial bool }

func (c cancelledChatCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		text := provider.AssistantMessage("partial")
		if c.partial && !yield(&provider.Completion{Message: &text}, nil) {
			return
		}
		yield(&provider.Completion{Status: provider.CompletionStatusFailed, StopDetails: &provider.StopDetails{Type: "cancelled"}}, nil)
	}
}

func TestChatReportsFailedProviderStatus(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/partial=%t", stream, partial), func(t *testing.T) {
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("test", cancelledChatCompleter{partial: partial})
				body := fmt.Sprintf(`{"model":"test","messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream)
				rec := httptest.NewRecorder()
				New(cfg).handleChatCompletion(rec, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(body)))
				want := http.StatusBadGateway
				if stream && partial {
					want = http.StatusOK
				}
				wire := rec.Body.String()
				if rec.Code != want || !strings.Contains(wire, `"error"`) || !strings.Contains(wire, "cancelled") || strings.Contains(wire, "[DONE]") || strings.Contains(wire, `"finish_reason":"stop"`) {
					t.Fatalf("HTTP %d: %s", rec.Code, wire)
				}
			})
		}
	}
}
