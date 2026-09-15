package responses

import (
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

type unphasedCompleter struct{}

func (unphasedCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("I will check that now.")}}, StopReason: provider.StopReasonEndTurn}, nil)
	}
}

func TestResponsesDoNotInventNativePhase(t *testing.T) {
	for _, stream := range []string{"false", "true"} {
		t.Run(stream, func(t *testing.T) {
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("native", unphasedCompleter{})
			rec := httptest.NewRecorder()
			New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"native","input":"check","stream":`+stream+`}`)))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "I will check that now.") {
				t.Fatalf("response=%s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), `"phase"`) {
				t.Fatalf("invented message phase: %s", rec.Body.String())
			}
		})
	}
}
