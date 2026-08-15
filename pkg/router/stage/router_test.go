package stage

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type mockCompleter struct {
	text  string
	err   error
	calls int
}

func (m *mockCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls++
		if m.err != nil {
			yield(nil, m.err)
			return
		}
		yield(&provider.Completion{Message: &provider.Message{
			Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: m.text}},
		}}, nil)
	}
}

func newTestRouter(t *testing.T, options Options) (*Completer, *mockCompleter, *mockCompleter) {
	t.Helper()
	capable := &mockCompleter{text: "capable"}
	efficient := &mockCompleter{text: "efficient"}
	router, err := NewCompleter(
		Candidate{Model: "capable", Completer: capable},
		Candidate{Model: "efficient", Completer: efficient},
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	return router, capable, efficient
}

func toolCall(name, arguments string) provider.Message {
	return provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{
		ToolCall: &provider.ToolCall{ID: name, Name: name, Arguments: arguments},
	}}}
}

func routeText(t *testing.T, router *Completer, messages []provider.Message) string {
	t.Helper()
	var output strings.Builder
	for completion, err := range router.Complete(context.Background(), messages, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if completion != nil && completion.Message != nil {
			output.WriteString(completion.Message.Text())
		}
	}
	return output.String()
}

func TestNoSignalUsesConfiguredDefault(t *testing.T) {
	router, _, _ := newTestRouter(t, Options{Picker: EfficientFirst, ConfidenceThreshold: 0.5})
	decision := router.Route([]provider.Message{provider.UserMessage("implement this")})
	if decision.Tier != TierEfficient || decision.Source != "no_signal" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestCriticalFailureForcesCapable(t *testing.T) {
	router, _, _ := newTestRouter(t, Options{Picker: EfficientFirst, ConfidenceThreshold: 1})
	messages := []provider.Message{
		provider.UserMessage("fix it"),
		provider.ToolMessage("t1", "fatal: connection refused while starting service"),
	}
	decision := router.Route(messages)
	if decision.Tier != TierCapable || decision.Source != "override" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestPassingTestsAfterEditUseEfficient(t *testing.T) {
	router, _, _ := newTestRouter(t, Options{Picker: CapableFirst, ConfidenceThreshold: 1})
	messages := []provider.Message{
		provider.UserMessage("fix it"),
		toolCall("apply_patch", "{}"),
		provider.ToolMessage("t1", "all tests passed in 0.4s; 0 failed"),
	}
	decision := router.Route(messages)
	if decision.Tier != TierEfficient || decision.Source != "tests_passed" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestExplorationAndErrorsCorroborateEscalation(t *testing.T) {
	router, _, _ := newTestRouter(t, Options{Picker: EfficientFirst, ConfidenceThreshold: 0.5})
	messages := []provider.Message{
		provider.UserMessage("fix it"), provider.AssistantMessage("looking"),
		toolCall("read", `{"path":"a.go"}`), provider.ToolMessage("r1", "source"),
		provider.AssistantMessage("more"), toolCall("read", `{"path":"b.go"}`),
		provider.ToolMessage("r2", "AssertionError"), provider.UserMessage("continue"),
	}
	decision := router.Route(messages)
	if decision.Tier != TierCapable || decision.Source != "dimensions" || decision.Confidence < 0.5 {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestSelectedFailureFallsBackToOtherTier(t *testing.T) {
	router, _, efficient := newTestRouter(t, Options{Picker: EfficientFirst})
	efficient.err = errors.New("provider down")
	if output := routeText(t, router, []provider.Message{provider.UserMessage("hello")}); output != "capable" {
		t.Fatalf("expected capable fallback, got %q", output)
	}
}

func TestValidation(t *testing.T) {
	mock := &mockCompleter{}
	if _, err := NewCompleter(Candidate{Model: "same", Completer: mock}, Candidate{Model: "same", Completer: mock}, Options{}); err == nil {
		t.Fatal("expected duplicate model error")
	}
	if _, err := NewCompleter(Candidate{Model: "a", Completer: mock}, Candidate{Model: "b", Completer: mock}, Options{ConfidenceThreshold: 1.1}); err == nil {
		t.Fatal("expected threshold error")
	}
}
