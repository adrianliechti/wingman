package decider

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"math"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type completerFunc func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error]

func (f completerFunc) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return f(ctx, messages, options)
}

type embedderFunc func(context.Context, []string, *provider.EmbedOptions) (*provider.Embedding, error)

func (f embedderFunc) Embed(ctx context.Context, texts []string, options *provider.EmbedOptions) (*provider.Embedding, error) {
	return f(ctx, texts, options)
}

func decisionInput() *provider.DecisionInput {
	return &provider.DecisionInput{
		State: map[string]any{"ticket": "Payment failed", "id": json.Number("9007199254740993")},
		Questions: []provider.DecisionQuestion{
			{ID: "private-noul-id", Instructions: map[string]any{"question": "Is it urgent?"}, Noul: &provider.NoulQuestion{True: []any{"Act now"}}},
			{ID: "private-choice-id", Instructions: "Which department?", Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{
				{Label: "billing", Description: map[string]any{"examples": []any{"payment"}}}, {Label: "other"},
			}}},
			{ID: "private-score-id", Instructions: []any{"Rate severity"}, Score: &provider.ScoreQuestion{Levels: []any{nil, "Medium", map[string]any{"severity": "High"}}}},
		},
	}
}

func TestCompleterDecision(t *testing.T) {
	input := decisionInput()
	backend := completerFunc(func(ctx context.Context, messages []provider.Message, opts *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
		if opts.Schema == nil || opts.Schema.Strict == nil || !*opts.Schema.Strict {
			t.Fatal("expected strict structured output")
		}
		prompt := messages[1].Text()
		for _, expected := range []string{"9007199254740993", `"examples":["payment"]`, `"question":"Is it urgent?"`, `"Rate severity"`} {
			if !strings.Contains(prompt, expected) {
				t.Errorf("prompt lost structured input %q: %s", expected, prompt)
			}
		}
		if strings.Contains(prompt, "private-") {
			t.Error("application question IDs should not enter the prompt")
		}
		return func(yield func(*provider.Completion, error) bool) {
			if !yield(&provider.Completion{Message: &provider.Message{Content: []provider.Content{
				{MessageID: "comment", Phase: provider.MessagePhaseCommentary, Text: `{"not":"the answer"}`},
			}}}, nil) {
				return
			}
			for _, part := range []string{`{"q0":{"0":0,"1":1},`, `"q1":{"0":0.6,"1":0.2},"q2":{"0":0,"1":0.75,"2":0.25}}`} {
				if !yield(&provider.Completion{
					ID: "decision-id", Model: "actual-model",
					Message: &provider.Message{Content: []provider.Content{{MessageID: "final", Phase: provider.MessagePhaseFinalAnswer, Text: part}}},
					Usage:   &provider.Usage{InputTokens: 42, OutputTokens: 10},
				}, nil) {
					return
				}
			}
		}
	})
	result, err := FromCompleter("alias", backend).Decide(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "decision-id" || result.Model != "actual-model" || result.Usage.InputTokens != 42 || result.Usage.OutputTokens != 10 {
		t.Fatalf("metadata: %+v", result)
	}
	if len(result.Answers) != 3 || result.Answers[0].ID != input.Questions[0].ID || result.Answers[0].Noul.Probability != 0 {
		t.Fatalf("noul answer: %+v", result.Answers)
	}
	choice := result.Answers[1].Choice
	if choice.Choice != "billing" || math.Abs(choice.Probabilities["billing"]-0.75) > 1e-12 {
		t.Fatalf("choice answer: %+v", choice)
	}
	score := result.Answers[2].Score
	if score.Score != 1.25 || len(score.Levels) != 3 || score.Levels[0].Description != nil {
		t.Fatalf("score answer: %+v", score)
	}
	if choice.Confidence <= 0 || choice.Confidence >= 1 || score.Confidence <= 0 || score.Confidence >= 1 {
		t.Fatal("expected nontrivial distribution confidence")
	}
}

func TestCompleterRejectsInvalidDecisions(t *testing.T) {
	for _, output := range []string{
		`{}`, `null`, `{"q0":{"0":0,"1":0}}`, `{"q0":{"0":null,"1":1}}`,
		`{"q0":{"0":-0.1,"1":1}}`, `{"q0":{"0":1.1,"1":0}}`,
		`{"q0":{"0":0.5,"wrong":0.5}}`, `{"q0":{"0":0.5}}`,
		`{"q0":{"0":0.5,"1":0.5,"2":0}}`, `{"q0":{"0":"yes","1":0}}`,
		`{"q0":{"0":0.5,"1":0.5},"extra":{}}`, "not JSON",
	} {
		t.Run(output, func(t *testing.T) {
			backend := completerFunc(func(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
				return func(yield func(*provider.Completion, error) bool) {
					message := provider.AssistantMessage(output)
					yield(&provider.Completion{Message: &message}, nil)
				}
			})
			input := decisionInput()
			input.Questions = input.Questions[:1]
			if _, err := FromCompleter("model", backend).Decide(context.Background(), input); err == nil {
				t.Fatal("expected invalid model output to fail")
			}
		})
	}
}

func TestDecisionConfidence(t *testing.T) {
	q := decisionInput().Questions[1]
	for _, tc := range []struct {
		weights []float64
		want    float64
	}{{[]float64{0.5, 0.5}, 0}, {[]float64{1, 0}, 1}} {
		result, err := answer(q, tc.weights)
		if err != nil || result.Choice.Confidence != tc.want {
			t.Fatalf("confidence for %v = %+v, %v", tc.weights, result, err)
		}
	}
}

func TestEmbedderDecision(t *testing.T) {
	backend := embedderFunc(func(ctx context.Context, texts []string, opts *provider.EmbedOptions) (*provider.Embedding, error) {
		if len(texts) != 13 || !strings.Contains(texts[0], "9007199254740993") || !strings.Contains(texts[6], "Which department?") || texts[5] != "examples: - payment" {
			t.Fatalf("embedding inputs: %v", texts)
		}
		for _, text := range texts {
			if strings.Contains(text, "private-") {
				t.Fatal("application question IDs should not enter embedding inputs")
			}
		}
		if strings.Contains(texts[0], "Which department?") || !strings.Contains(texts[0], "Payment failed") {
			t.Fatalf("expected standalone state: %q", texts[0])
		}
		if !strings.Contains(texts[9], `"id":9007199254740993`) || !strings.Contains(texts[10], `"answer":"0"`) || !strings.Contains(texts[12], `"answer":"2"`) {
			t.Fatalf("Score must retain its structured state and ordered levels: %v", texts[9:])
		}
		return &provider.Embedding{
			Model: "embedding-model", Usage: &provider.Usage{InputTokens: 55},
			Embeddings: [][]float32{
				{1, 0},                          // State.
				{1, 0}, {-1, 0}, {0, 1}, {0, 1}, // Noul: both averages are zero.
				{1, 0}, {1, 0}, {0, 1}, {0, 1}, // Choice: averages 1 and 0.
				{0, 1}, {0, -1}, {1, 0}, {0, 1}, // Score state and levels: -1, 0, 1.
			},
		}, nil
	})
	result, err := FromEmbedder("alias", backend).Decide(context.Background(), decisionInput())
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "embedding-model" || result.Usage.InputTokens != 55 || result.Usage.OutputTokens != 0 || len(result.Answers) != 3 {
		t.Fatalf("result: %+v", result)
	}
	if result.Answers[0].Noul.Probability != 0.5 {
		t.Fatalf("equal similarities should give noul 0.5: %+v", result.Answers[0])
	}
	choice := result.Answers[1].Choice
	if choice.Choice != "billing" || math.Abs(choice.Probabilities["billing"]-math.E/(math.E+1)) > 1e-12 {
		t.Fatalf("choice: %+v", choice)
	}
	score := result.Answers[2].Score
	want := (math.Exp(-1) + 2) / (math.Exp(-2) + math.Exp(-1) + 1)
	if math.Abs(score.Score-want) > 1e-12 {
		t.Fatalf("score = %v, want %v", score.Score, want)
	}
}

func TestEmbeddingTextPreservesStructuredContent(t *testing.T) {
	value := struct {
		Details map[string]any `json:"details"`
	}{Details: map[string]any{
		"number": json.Number("9007199254740993"),
		"items":  []any{"first", map[string]any{"name": "second", "enabled": false}, nil},
	}}
	text, err := embeddingText(value)
	if err != nil {
		t.Fatal(err)
	}
	want := "details:\n  items:\n    - first\n    - enabled: false\n      name: second\n    - null\n  number: 9007199254740993"
	if text != want {
		t.Fatalf("structured embedding text = %q, want %q", text, want)
	}
}

func TestEmbedderReusesCriteriaWithoutInstructions(t *testing.T) {
	input := &provider.DecisionInput{State: map[string]any{"text": "Payment failed"}, Questions: []provider.DecisionQuestion{
		{ID: "first", Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: "billing", Description: "Payment"}, {Label: "other"}}}},
		{ID: "second", Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: "different-id", Description: "Payment"}, {Label: "other"}}}},
	}}
	backend := embedderFunc(func(_ context.Context, texts []string, _ *provider.EmbedOptions) (*provider.Embedding, error) {
		if len(texts) != 3 || texts[0] != "Payment failed" || texts[1] != "Payment" || texts[2] != "other" {
			t.Fatalf("expected unwrapped state and deduplicated criteria: %v", texts)
		}
		return &provider.Embedding{Embeddings: [][]float32{{1, 0}, {1, 0}, {0, 1}}}, nil
	})
	result, err := FromEmbedder("model", backend).Decide(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Answers[0].Choice.Choice != "billing" || result.Answers[1].Choice.Choice != "different-id" {
		t.Fatalf("reused criteria lost their question/label mappings: %+v", result.Answers)
	}
}

func TestEmbedderEmptyInputs(t *testing.T) {
	for _, state := range []map[string]any{nil, {}, {"text": ""}, {"items": []any{}}} {
		input := &provider.DecisionInput{State: state, Questions: []provider.DecisionQuestion{
			{ID: "empty", Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: ""}}}},
		}}
		backend := embedderFunc(func(_ context.Context, texts []string, _ *provider.EmbedOptions) (*provider.Embedding, error) {
			vectors := make([][]float32, len(texts))
			for i, text := range texts {
				if strings.TrimSpace(text) == "" {
					t.Fatal("valid empty inputs must not send empty embedding texts")
				}
				vectors[i] = []float32{1}
			}
			return &provider.Embedding{Embeddings: vectors}, nil
		})
		result, err := FromEmbedder("model", backend).Decide(context.Background(), input)
		if err != nil || result.Answers[0].Choice.Probabilities[""] != 1 {
			t.Fatalf("empty-input decision: %+v, %v", result, err)
		}
	}
}

func TestEmbedderRejectsInvalidVectors(t *testing.T) {
	for name, vectors := range map[string][][]float32{
		"missing": nil, "dimensions": {{1}, {1, 0}, {1}, {1}, {1}}, "empty": {{}, {}, {}, {}, {}},
		"zero": {{0}, {1}, {1}, {1}, {1}}, "nan": {{1}, {float32(math.NaN())}, {1}, {1}, {1}},
		"infinity": {{1}, {1}, {float32(math.Inf(1))}, {1}, {1}},
	} {
		t.Run(name, func(t *testing.T) {
			backend := embedderFunc(func(context.Context, []string, *provider.EmbedOptions) (*provider.Embedding, error) {
				return &provider.Embedding{Embeddings: vectors}, nil
			})
			input := decisionInput()
			input.Questions = input.Questions[:1]
			if _, err := FromEmbedder("model", backend).Decide(context.Background(), input); err == nil {
				t.Fatal("expected invalid embeddings to fail")
			}
		})
	}
}

func TestDecisionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := embedderFunc(func(ctx context.Context, _ []string, _ *provider.EmbedOptions) (*provider.Embedding, error) {
		return nil, ctx.Err()
	})
	if _, err := FromEmbedder("model", backend).Decide(ctx, decisionInput()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
