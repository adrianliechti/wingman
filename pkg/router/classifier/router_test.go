package classifier

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// mockCompleter records how many times it is invoked and returns either a fixed
// text, an error before any output, or nothing at all (empty). With prelude
// set, it first yields a role-only chunk, as real streaming providers do.
type mockCompleter struct {
	name    string
	text    string
	err     error
	prelude bool
	empty   bool
	calls   int
}

func (m *mockCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls++

		if m.prelude {
			role := &provider.Completion{
				Message: &provider.Message{Role: provider.MessageRoleAssistant},
			}

			if !yield(role, nil) {
				return
			}
		}

		if m.err != nil {
			yield(nil, m.err)
			return
		}

		if m.empty {
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

// mockEmbedder maps each text to a vector via vec. It is safe for concurrent
// use (the router pre-warms centroids from a goroutine) and records every
// embedded text.
type mockEmbedder struct {
	vec func(string) []float32

	mu    sync.Mutex
	texts []string
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string, options *provider.EmbedOptions) (*provider.Embedding, error) {
	m.mu.Lock()
	m.texts = append(m.texts, texts...)
	m.mu.Unlock()

	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.vec(t)
	}

	return &provider.Embedding{Embeddings: out}, nil
}

func (m *mockEmbedder) embedded(text string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.texts {
		if t == text {
			return true
		}
	}

	return false
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

// ambiguousMsg scores 2.5 (base 1 + medium 0.5 + two hard words 1) — exactly
// on the boundary of a MaxDifficulty-2 candidate, so the cascade escalates.
func ambiguousMsg() ([]provider.Message, *provider.CompleteOptions) {
	return userMsg("please refactor the architecture"), reasoning(provider.EffortMedium)
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

	// The centroid pre-warm may embed the examples, but a confident task must
	// never embed the query or consult the judge.
	if embedder.embedded("hi") || judge.calls != 0 {
		t.Fatalf("confident task must not escalate: judge=%d", judge.calls)
	}
}

func TestAmbiguousEscalatesToJudge(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}
	// Judge overrides the heuristic pick, choosing option 0 (cheap).
	judge := &mockCompleter{name: "judge", text: `{"model_index":0}`}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{Judge: judge})
	if err != nil {
		t.Fatal(err)
	}

	messages, options := ambiguousMsg()

	out, err := drain(c.Complete(context.Background(), messages, options))
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

func TestJudgeAnswersRenumberedIndex(t *testing.T) {
	a := &mockCompleter{name: "a"}
	b := &mockCompleter{name: "b"}
	d := &mockCompleter{name: "d"}

	// The judge answers with ordinal option 1. Eligible is [0, 2] (candidate 1
	// lacks vision), so option 1 must map back to candidate 2.
	judge := &mockCompleter{name: "judge", text: `{"model_index":1}`}

	c, err := NewCompleter([]Candidate{
		{Completer: a, Model: "a", Cost: 1, MaxDifficulty: 2, Vision: true},
		{Completer: b, Model: "b", Cost: 5, MaxDifficulty: 3, Vision: false},
		{Completer: d, Model: "d", Cost: 60, MaxDifficulty: 4, Vision: true},
	}, Options{Judge: judge})
	if err != nil {
		t.Fatal(err)
	}

	messages := []provider.Message{{
		Role: provider.MessageRoleUser,
		Content: []provider.Content{
			{Text: "please refactor the architecture"},
			{File: &provider.File{ContentType: "image/png"}},
		},
	}}

	out, err := drain(c.Complete(context.Background(), messages, reasoning(provider.EffortMedium)))
	if err != nil {
		t.Fatal(err)
	}

	if judge.calls != 1 {
		t.Fatalf("expected judge to be consulted once, got %d", judge.calls)
	}

	if out != "d" {
		t.Fatalf("expected renumbered option 1 to map to candidate d, got %q", out)
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
	}, Options{Embedder: embedder})
	if err != nil {
		t.Fatal(err)
	}

	// Ambiguous difficulty and lexically a "beta" task: the beta centroid wins
	// by a decisive margin.
	out, err := drain(c.Complete(context.Background(), userMsg("please refactor the architecture of the beta module"), reasoning(provider.EffortMedium)))
	if err != nil {
		t.Fatal(err)
	}

	if out != "strong" {
		t.Fatalf("expected embedding to route to strong, got %q", out)
	}
}

func TestEmbeddingWithoutMarginKeepsHeuristic(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	// Every text embeds identically: the argmax carries no signal and must not
	// override the heuristic pick (level 3 => strong).
	embedder := &mockEmbedder{vec: func(string) []float32 { return []float32{0.5, 0.5} }}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2, Examples: []string{"alpha task"}},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4, Examples: []string{"beta task"}},
	}, Options{Embedder: embedder})
	if err != nil {
		t.Fatal(err)
	}

	messages, options := ambiguousMsg()

	out, err := drain(c.Complete(context.Background(), messages, options))
	if err != nil {
		t.Fatal(err)
	}

	if out != "strong" {
		t.Fatalf("indecisive embedding must keep the heuristic pick, got %q", out)
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

func TestFallbackAfterRoleOnlyChunk(t *testing.T) {
	cheap := &mockCompleter{name: "cheap", err: context.DeadlineExceeded, prelude: true}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{DefaultIndex: 1})
	if err != nil {
		t.Fatal(err)
	}

	// A role-only first chunk carries no output; a stream dying right after it
	// must still fall back.
	out, err := drain(c.Complete(context.Background(), userMsg("hello"), nil))
	if err != nil {
		t.Fatalf("fallback should swallow the error, got %v", err)
	}

	if out != "strong" {
		t.Fatalf("expected fallback to strong, got %q", out)
	}
}

func TestEmptyStreamFallsBack(t *testing.T) {
	cheap := &mockCompleter{name: "cheap", empty: true, prelude: true}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{DefaultIndex: 1})
	if err != nil {
		t.Fatal(err)
	}

	// A stream that completes without content is an empty answer and must be
	// retried on the fallback.
	out, err := drain(c.Complete(context.Background(), userMsg("hello"), nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "strong" {
		t.Fatalf("expected fallback on empty answer, got %q", out)
	}

	if cheap.calls != 1 || strong.calls != 1 {
		t.Fatalf("calls: cheap=%d strong=%d", cheap.calls, strong.calls)
	}
}

func TestFallbackPrefersEligible(t *testing.T) {
	novision := &mockCompleter{name: "novision"}
	visA := &mockCompleter{name: "vis-a", err: context.DeadlineExceeded}
	visB := &mockCompleter{name: "vis-b"}

	// The default candidate lacks vision: an image request whose pick fails
	// must fall back to the other vision candidate, never to the default.
	c, err := NewCompleter([]Candidate{
		{Completer: novision, Model: "novision", Cost: 1, MaxDifficulty: 4, Vision: false},
		{Completer: visA, Model: "vis-a", Cost: 1, MaxDifficulty: 2, Vision: true},
		{Completer: visB, Model: "vis-b", Cost: 60, MaxDifficulty: 4, Vision: true},
	}, Options{DefaultIndex: 0})
	if err != nil {
		t.Fatal(err)
	}

	messages := []provider.Message{{
		Role: provider.MessageRoleUser,
		Content: []provider.Content{
			{Text: "hello"},
			{File: &provider.File{ContentType: "image/png"}},
		},
	}}

	out, err := drain(c.Complete(context.Background(), messages, nil))
	if err != nil {
		t.Fatalf("fallback should swallow the error, got %v", err)
	}

	if out != "vis-b" {
		t.Fatalf("expected eligible fallback vis-b, got %q", out)
	}

	if novision.calls != 0 {
		t.Fatal("non-vision default must never see an image request")
	}
}

func TestStablePickSkipsEscalation(t *testing.T) {
	pricey := &mockCompleter{name: "pricey"}
	strong := &mockCompleter{name: "strong"}
	judge := &mockCompleter{name: "judge", text: `{"model_index":0}`}

	// The strong candidate is also the cheapest, so it wins on both sides of
	// the difficulty boundary: the score is near 2.5, but escalation could not
	// change the outcome and the judge must stay silent.
	c, err := NewCompleter([]Candidate{
		{Completer: pricey, Model: "pricey", Cost: 10, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 1, MaxDifficulty: 4},
	}, Options{Judge: judge})
	if err != nil {
		t.Fatal(err)
	}

	messages, options := ambiguousMsg()

	out, err := drain(c.Complete(context.Background(), messages, options))
	if err != nil {
		t.Fatal(err)
	}

	if out != "strong" {
		t.Fatalf("expected strong, got %q", out)
	}

	if judge.calls != 0 {
		t.Fatalf("stable pick must not consult the judge, got %d calls", judge.calls)
	}
}

func TestLongTrivialConversationStaysCheap(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// ~35k tokens of harmless history and a trivial follow-up: sheer context
	// size must not buy the expensive model — that's where input cost hurts
	// the most.
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 5200)

	messages := []provider.Message{
		provider.UserMessage(filler),
		provider.AssistantMessage("noted."),
		provider.UserMessage("thanks, looks good!"),
	}

	out, err := drain(c.Complete(context.Background(), messages, nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "cheap" {
		t.Fatalf("long but trivial conversation must stay cheap, got %q", out)
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

	messages, options := ambiguousMsg()

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
	turn1 := []provider.Message{provider.UserMessage("please refactor the architecture")}

	// Second request in the same task: the instruction plus an assistant tool
	// call and a (text-less) tool result appended by the agent loop.
	turn2 := []provider.Message{
		provider.UserMessage("please refactor the architecture"),
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

func TestCachedDecisionRevalidatesEligibility(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 4, MaxContext: 1000},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Turn 1 fits cheap's context window and is cached.
	turn1 := []provider.Message{provider.UserMessage("summarize the report")}

	if out, _ := drain(c.Complete(context.Background(), turn1, nil)); out != "cheap" {
		t.Fatalf("expected cheap for the small request, got %q", out)
	}

	// The task's tool round-trip grows the context past cheap's MaxContext;
	// the same fingerprint must not reuse the now-ineligible pick.
	turn2 := []provider.Message{
		provider.UserMessage("summarize the report"),
		provider.ToolMessage("t1", strings.Repeat("x", 8000)),
	}

	if out, _ := drain(c.Complete(context.Background(), turn2, nil)); out != "strong" {
		t.Fatalf("expected re-route to strong after context growth, got %q", out)
	}
}

func TestSystemPromptDoesNotInflateDifficulty(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// A typical agent platform request: a huge static system prompt full of
	// code fences and demanding vocabulary, a large tool set — and a trivial
	// user turn.
	instructions := strings.Repeat(
		"Use the debug skill to find the root cause. Refactor the architecture.\n"+
			"```python\nprint('example')\n```\n", 600)

	messages := []provider.Message{
		provider.SystemMessage(instructions),
		provider.UserMessage("hi"),
	}

	options := &provider.CompleteOptions{
		Tools: make([]provider.Tool, 18),
	}

	out, err := drain(c.Complete(context.Background(), messages, options))
	if err != nil {
		t.Fatal(err)
	}

	if out != "cheap" {
		t.Fatalf("trivial task must not route by system prompt weight, got %q", out)
	}

	s := extractSignals(messages, options)

	if s.taskTokens >= s.approxTokens {
		t.Fatalf("system text must count toward approxTokens only: task=%d approx=%d", s.taskTokens, s.approxTokens)
	}

	if s.recentFences != 0 || s.historyFences != 0 || s.recentHard != 0 || s.historyHard != 0 {
		t.Fatalf("system text must not feed lexical signals: %+v", s)
	}
}

func TestConversationDeescalates(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// A conversation whose heavy lifting is over: the history holds hard words
	// and code, but the current turn is trivial.
	messages := []provider.Message{
		provider.UserMessage("refactor the distributed architecture of this service"),
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "Here is the refactored code:\n```go\nfunc main() {}\n```"}}},
		provider.UserMessage("thanks, looks good!"),
	}

	out, err := drain(c.Complete(context.Background(), messages, nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "cheap" {
		t.Fatalf("simple follow-up must de-escalate, got %q", out)
	}
}

func TestUserTriggerEscalates(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	out, err := drain(c.Complete(context.Background(), userMsg("take your time and think hard: design a distributed cache"), nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "strong" {
		t.Fatalf("explicit depth request must escalate, got %q", out)
	}
}

func TestUserTriggerDeescalates(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}

	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 60, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	messages := []provider.Message{
		provider.UserMessage("debug this race condition in the scheduler"),
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "The fix:\n```go\nmu.Lock()\n```"}}},
		provider.UserMessage("quick question: what does the -race flag do?"),
	}

	out, err := drain(c.Complete(context.Background(), messages, nil))
	if err != nil {
		t.Fatal(err)
	}

	if out != "cheap" {
		t.Fatalf("explicit brevity request must de-escalate, got %q", out)
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

func TestContainsWordBoundaries(t *testing.T) {
	if containsWord("please improve this text", "prove") {
		t.Fatal("prove must not match inside improve")
	}

	if !containsWord("prove this theorem", "prove") {
		t.Fatal("prove should match at a word start")
	}

	if !containsWord("the architecture is sound", "architect") {
		t.Fatal("prefix matches should still work")
	}

	if s := extractSignals(userMsg("improve my email wording"), nil); s.recentHard != 0 {
		t.Fatalf("improve must not count as a hard word, got %d", s.recentHard)
	}
}

func TestTruncateTextRuneSafe(t *testing.T) {
	if got := truncateText("héllo", 2); got != "h" {
		t.Fatalf("expected rune-safe cut, got %q", got)
	}

	if got := truncateText("hello", 3); got != "hel" {
		t.Fatalf("expected plain cut, got %q", got)
	}

	if got := truncateText("hi", 10); got != "hi" {
		t.Fatalf("expected unchanged, got %q", got)
	}
}
