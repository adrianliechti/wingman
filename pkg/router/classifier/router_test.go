package classifier

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// mockCompleter records how many times it is invoked and returns either a fixed
// text or an error before any output.
type mockCompleter struct {
	name  string
	text  string
	err   error
	calls int
}

func (m *mockCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls++

		if m.err != nil {
			yield(nil, m.err)
			return
		}

		text := m.text
		if text == "" {
			text = m.name
		}

		yield(&provider.Completion{
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{{Text: text}},
			},
		}, nil)
	}
}

// mockEmbedder maps each text to a vector via vec and counts calls.
type mockEmbedder struct {
	vec   func(string) []float32
	calls int
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string, options *provider.EmbedOptions) (*provider.Embedding, error) {
	m.calls++

	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.vec(t)
	}

	return &provider.Embedding{Embeddings: out}, nil
}

func drain(seq iter.Seq2[*provider.Completion, error]) (string, error) {
	var sb strings.Builder
	var rerr error

	for c, err := range seq {
		if err != nil {
			rerr = err
		}

		if c != nil && c.Message != nil {
			sb.WriteString(c.Message.Text())
		}
	}

	return sb.String(), rerr
}

func userMsg(text string) []provider.Message {
	return []provider.Message{provider.UserMessage(text)}
}

func reasoning(effort provider.Effort) *provider.CompleteOptions {
	return &provider.CompleteOptions{
		ReasoningOptions: &provider.ReasoningOptions{Effort: effort},
	}
}

func TestNewCompleterRequiresCandidate(t *testing.T) {
	if _, err := NewCompleter(nil, Options{}); err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestEasyTaskRoutesToCheapest(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	out, err := drain(c.Complete(context.Background(), userMsg("hello there"), nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "cheap" {
		t.Fatalf("expected cheap, got %q", out)
	}

	if cheap.calls != 1 || strong.calls != 0 {
		t.Fatalf("calls: cheap=%d strong=%d", cheap.calls, strong.calls)
	}
}

func TestHardTaskRoutesToStrong(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// xhigh effort (+3) on top of base 1 => level 4; only strong clears it.
	out, err := drain(c.Complete(context.Background(), userMsg("refactor the architecture"), reasoning(provider.EffortXHigh)))
	if err != nil {
		t.Fatal(err)
	}

	if out != "strong" {
		t.Fatalf("expected strong, got %q", out)
	}

	if strong.calls != 1 || cheap.calls != 0 {
		t.Fatalf("calls: cheap=%d strong=%d", cheap.calls, strong.calls)
	}
}

func TestImageNeverRoutesToNonVision(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	vision := &mockCompleter{name: "vision"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 4, Vision: false},
		{Completer: vision, Model: "vision", Cost: 60, MaxDifficulty: 4, Vision: true},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	messages := []provider.Message{{
		Role: provider.MessageRoleUser,
		Content: []provider.Content{
			{Text: "what is in this picture"},
			{File: &provider.File{ContentType: "image/png"}},
		},
	}}

	out, err := drain(c.Complete(context.Background(), messages, nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "vision" {
		t.Fatalf("expected vision, got %q", out)
	}

	if cheap.calls != 0 {
		t.Fatalf("non-vision candidate must not be called for an image task")
	}
}

func TestConfidentTaskSkipsEmbedderAndJudge(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}
	judge := &mockCompleter{name: "judge", text: `{"model_index":1}`}
	embedder := &mockEmbedder{vec: func(string) []float32 { return []float32{1, 0} }}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2, Examples: []string{"x"}},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4, Examples: []string{"y"}},
	}, Options{Embedder: embedder, Judge: judge})
	if err != nil {
		t.Fatal(err)
	}

	out, err := drain(c.Complete(context.Background(), userMsg("hi"), nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "cheap" {
		t.Fatalf("expected cheap, got %q", out)
	}

	if embedder.calls != 0 || judge.calls != 0 {
		t.Fatalf("confident task must not escalate: embedder=%d judge=%d", embedder.calls, judge.calls)
	}
}

func TestAmbiguousEscalatesToJudge(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}
	// Judge overrides the heuristic pick, choosing index 0 (cheap).
	judge := &mockCompleter{name: "judge", text: `{"model_index":0}`}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{Judge: judge})
	if err != nil {
		t.Fatal(err)
	}

	// medium effort (+1) + one hard word (+0.5) over base 1 => score 2.5,
	// exactly on cheap's MaxDifficulty boundary => ambiguous.
	out, err := drain(c.Complete(context.Background(), userMsg("please refactor this"), reasoning(provider.EffortMedium)))
	if err != nil {
		t.Fatal(err)
	}

	if judge.calls != 1 {
		t.Fatalf("expected judge to be consulted once, got %d", judge.calls)
	}

	if out != "cheap" {
		t.Fatalf("expected judge's choice (cheap), got %q", out)
	}
}

func TestEmbeddingResolvesAmbiguous(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	embedder := &mockEmbedder{vec: func(t string) []float32 {
		t = strings.ToLower(t)
		switch {
		case strings.Contains(t, "alpha"):
			return []float32{1, 0}
		case strings.Contains(t, "beta"):
			return []float32{0, 1}
		default:
			return []float32{0.5, 0.5}
		}
	}}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2, Examples: []string{"alpha task"}},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4, Examples: []string{"beta task"}},
	}, Options{Embedder: embedder, Threshold: 0.75})
	if err != nil {
		t.Fatal(err)
	}

	// Ambiguous difficulty (medium + one hard word) and lexically a "beta" task.
	out, err := drain(c.Complete(context.Background(), userMsg("please refactor the beta module"), reasoning(provider.EffortMedium)))
	if err != nil {
		t.Fatal(err)
	}

	if out != "strong" {
		t.Fatalf("expected embedding to route to strong, got %q", out)
	}

	if embedder.calls == 0 {
		t.Fatal("expected embedder to be consulted")
	}
}

func TestFallbackOnErrorBeforeOutput(t *testing.T) {
	cheap := &mockCompleter{name: "cheap", err: context.DeadlineExceeded}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{DefaultIndex: 1})
	if err != nil {
		t.Fatal(err)
	}

	// Easy task picks cheap, which errors before output => fall back to default.
	out, err := drain(c.Complete(context.Background(), userMsg("hello"), nil))
	if err != nil {
		t.Fatalf("fallback should swallow the error, got %v", err)
	}

	if out != "strong" {
		t.Fatalf("expected fallback to strong, got %q", out)
	}

	if cheap.calls != 1 || strong.calls != 1 {
		t.Fatalf("calls: cheap=%d strong=%d", cheap.calls, strong.calls)
	}
}

func TestDecisionCacheDedupes(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}
	judge := &mockCompleter{name: "judge", text: `{"model_index":1}`}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{Judge: judge})
	if err != nil {
		t.Fatal(err)
	}

	messages := userMsg("please refactor this")
	options := reasoning(provider.EffortMedium)

	for i := 0; i < 3; i++ {
		if _, err := drain(c.Complete(context.Background(), messages, options)); err != nil {
			t.Fatal(err)
		}
	}

	if judge.calls != 1 {
		t.Fatalf("expected judge to be consulted once across identical requests, got %d", judge.calls)
	}
}

func TestDecisionStableAcrossToolRoundTrips(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}
	judge := &mockCompleter{name: "judge", text: `{"model_index":1}`}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{Judge: judge})
	if err != nil {
		t.Fatal(err)
	}

	options := reasoning(provider.EffortMedium)

	// First request: the bare instruction.
	turn1 := []provider.Message{provider.UserMessage("please refactor this")}

	// Second request in the same task: the instruction plus an assistant tool
	// call and a (text-less) tool result appended by the agent loop.
	turn2 := []provider.Message{
		provider.UserMessage("please refactor this"),
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{{ToolCall: &provider.ToolCall{ID: "t1", Name: "read"}}}},
		provider.ToolMessage("t1", "file contents"),
	}

	if _, err := drain(c.Complete(context.Background(), turn1, options)); err != nil {
		t.Fatal(err)
	}
	if _, err := drain(c.Complete(context.Background(), turn2, options)); err != nil {
		t.Fatal(err)
	}

	if judge.calls != 1 {
		t.Fatalf("expected one judge call across a task's round-trips, got %d", judge.calls)
	}
}

func TestDifficultyAndEligibility(t *testing.T) {
	easy := difficultyScore(extractSignals(userMsg("hi"), nil))
	if roundLevel(easy) > 1 {
		t.Fatalf("trivial task should be low difficulty, got %v", easy)
	}

	hard := difficultyScore(extractSignals(userMsg("refactor the distributed algorithm"), reasoning(provider.EffortMax)))
	if roundLevel(hard) < 3 {
		t.Fatalf("demanding task should be high difficulty, got %v", hard)
	}

	imgSignals := signals{hasImage: true}
	if isEligible(Candidate{Vision: false}, imgSignals) {
		t.Fatal("non-vision candidate should be ineligible for an image task")
	}
	if !isEligible(Candidate{Vision: true}, imgSignals) {
		t.Fatal("vision candidate should be eligible for an image task")
	}
}
