package decisions

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestFromDecisionRestoresValuesAndUsage(t *testing.T) {
	r := requestFor(t, `"x"`, `[{"type":"binary","id":"p","instructions":"x"},{"type":"choice","instructions":"x","choices":[{"value":false},{"value":"false"}]},{"type":"score","name":"s","instructions":"x","levels":[{"value":-2},{"value":8,"label":"high"}]},{"type":"predicate","instructions":"x"},{"type":"predicate","instructions":"x"}]`)
	input, err := r.ProviderInput()
	if err != nil {
		t.Fatal(err)
	}
	result := &provider.Decision{Usage: &provider.Usage{InputTokens: 100, OutputTokens: 1, CacheReadInputTokens: 20, CacheCreationInputTokens: 30, ReasoningTokens: new(0)}, Answers: []provider.DecisionAnswer{
		{ID: "0", Noul: &provider.NoulAnswer{Probability: 0}},
		{ID: "1", Choice: &provider.ChoiceAnswer{Choice: "0", Probabilities: map[string]float64{"0": 1, "1": 0}}},
		{ID: "2", Score: &provider.ScoreAnswer{Score: 0.5, Levels: []provider.ScoreLevel{{Probability: 0.75}, {Probability: 0.25}}}},
		{ID: "3", Refusal: &provider.RefusalAnswer{}},
		{ID: "4", Error: &provider.ErrorAnswer{Message: "failed"}},
	}}
	response, err := FromDecision(r, input, result)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(response)
	if response.Model != "m" || response.Usage.TotalTokens != 101 || response.Usage.InputTokensDetails.CachedTokens != 20 || response.Usage.InputTokensDetails.CacheWriteTokens != 30 || response.Answers[1].Choice != false || response.Answers[2].Probabilities[0].Value != -2 || response.Answers[2].Probabilities[0].Label != nil || response.Answers[3].Type != "refusal" || response.Answers[4].Message != "failed" || !strings.Contains(string(raw), `"probability":0`) {
		t.Fatalf("lost result values: %s", raw)
	}
}

func TestFromDecisionRejectsIncompleteAnswers(t *testing.T) {
	r := requestFor(t, `"x"`, `[{"type":"choice","instructions":"x","choices":[{"value":"a"},{"value":"b"}]}]`)
	input, err := r.ProviderInput()
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*provider.Decision{
		nil, {},
		{Answers: []provider.DecisionAnswer{{ID: "other", Noul: &provider.NoulAnswer{}}}},
		{Answers: []provider.DecisionAnswer{{ID: "0", Noul: &provider.NoulAnswer{}}}},
		{Answers: []provider.DecisionAnswer{{ID: "0", Choice: &provider.ChoiceAnswer{Choice: "0", Probabilities: map[string]float64{"0": 1}}}}},
		{Answers: []provider.DecisionAnswer{{ID: "0", Choice: &provider.ChoiceAnswer{Choice: "other", Probabilities: map[string]float64{"0": 1, "1": 0}}}}},
	} {
		if _, err := FromDecision(r, input, result); err == nil {
			t.Fatalf("accepted %+v", result)
		}
	}
	r = requestFor(t, `"x"`, `[{"type":"score","instructions":"x","levels":[{"label":"a"},{"label":"b"}]}]`)
	input, err = r.ProviderInput()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromDecision(r, input, &provider.Decision{Answers: []provider.DecisionAnswer{{ID: "0", Score: &provider.ScoreAnswer{}}}}); err == nil {
		t.Fatal("accepted missing score probabilities")
	}
}
