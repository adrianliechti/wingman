package auto

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
)

type decideFunc func(context.Context, *provider.DecisionInput) (*provider.Decision, error)

func (f decideFunc) Decide(ctx context.Context, in *provider.DecisionInput) (*provider.Decision, error) {
	return f(ctx, in)
}

type member struct {
	model string
	err   error
	calls int
}

func (m *member) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls++
		if m.err != nil {
			yield(nil, m.err)
			return
		}
		msg := provider.AssistantMessage(m.model)
		yield(&provider.Completion{Message: &msg}, nil)
	}
}

func TestDecisionFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer *provider.ChoiceAnswer
		err    error
		want   string
	}{
		{"confident", &provider.ChoiceAnswer{Choice: "B", Confidence: 0.9, Probabilities: map[string]float64{"A": 0.1, "B": 0.9}}, nil, "B"},
		{"tie", &provider.ChoiceAnswer{Choice: "B", Confidence: 0.5, Probabilities: map[string]float64{"A": 0.5, "B": 0.5}}, nil, "A"},
		{"uncertain", &provider.ChoiceAnswer{Choice: "B", Confidence: 0.3, Probabilities: map[string]float64{"A": 0.1, "B": 0.9}}, nil, "A"},
		{"unknown", &provider.ChoiceAnswer{Choice: "X", Confidence: 1, Probabilities: map[string]float64{"X": 1}}, nil, "A"},
		{"refused", nil, nil, "A"},
		{"timeout", nil, context.DeadlineExceeded, "A"},
		{"rejected", nil, provider.ErrRejected, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := &member{model: "A"}, &member{model: "B"}
			decider := decideFunc(func(ctx context.Context, in *provider.DecisionInput) (*provider.Decision, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > decisionTimeout {
					t.Error("missing decision deadline")
				}
				if err := in.Validate(); err != nil {
					t.Fatal(err)
				}
				answer := provider.DecisionAnswer{ID: questionID, Choice: tc.answer}
				if tc.answer == nil {
					answer.Refusal = &provider.RefusalAnswer{}
				}
				return &provider.Decision{Answers: []provider.DecisionAnswer{answer}}, tc.err
			})
			c, err := NewCompleter([]Route{{"A", "everyday", a}, {"B", "design", b}}, decider)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			var resultErr error
			for chunk, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("design a queue")}, nil) {
				resultErr = err
				if chunk != nil {
					got += chunk.Message.Text()
				}
			}
			if tc.err == provider.ErrRejected {
				if !errors.Is(resultErr, tc.err) || a.calls+b.calls != 0 {
					t.Fatal("rejection started a completion:", resultErr)
				}
			} else if resultErr != nil || got != tc.want || a.calls+b.calls != 1 {
				t.Fatalf("got %q, %v (%d calls), want %q", got, resultErr, a.calls+b.calls, tc.want)
			}
		})
	}
}

func TestFailoverAsksOnceAndSkipsOpenCircuit(t *testing.T) {
	a, b := &member{model: "A"}, &member{model: "B", err: errors.New("overload")}
	calls := 0
	d := decideFunc(func(context.Context, *provider.DecisionInput) (*provider.Decision, error) {
		calls++
		return &provider.Decision{Answers: []provider.DecisionAnswer{{ID: questionID, Choice: &provider.ChoiceAnswer{Choice: "B", Confidence: 1, Probabilities: map[string]float64{"A": 0, "B": 1}}}}}, nil
	})
	c, err := NewCompleter([]Route{{"A", "everyday", a}, {"B", "design", b}}, d, router.WithFailureThreshold(1))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for chunk, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("design")}, nil) {
			if err != nil || chunk.Message.Text() != "A" {
				t.Fatalf("failover: %v %v", chunk, err)
			}
		}
	}
	if calls != 1 || b.calls != 1 || a.calls != 2 {
		t.Fatalf("decisions=%d A=%d B=%d", calls, a.calls, b.calls)
	}
}

func TestBoundedToolContinuationEvidence(t *testing.T) {
	old := provider.AssistantMessage(strings.Repeat("界", 12000))
	messages := []provider.Message{provider.SystemMessage("system"), provider.UserMessage("discard"), old, old, old, old, provider.UserMessage("fix it"), provider.ToolMessage("id", "secret")}
	messages[len(messages)-1].Content[0].ToolResult.IsError = true
	state := requestEvidence(messages, nil)
	if state.Request != "fix it" || len(state.Recent) != 4 || state.ToolResults != 1 || state.FailedToolResults != 1 {
		t.Fatalf("invalid evidence for a tool continuation: %+v", state)
	}
	for _, m := range state.Recent {
		if len(m.Text) > 2048 || !utf8.ValidString(m.Text) || m.Role != provider.MessageRoleAssistant {
			t.Fatal("invalid history excerpt")
		}
	}
}
