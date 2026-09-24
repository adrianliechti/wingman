package provider

import "testing"

func TestDecisionInputValidation(t *testing.T) {
	for name, input := range map[string]*DecisionInput{
		"nil":               nil,
		"empty questions":   {State: map[string]any{}},
		"missing variant":   {Questions: []DecisionQuestion{{ID: "q"}}},
		"multiple variants": {Questions: []DecisionQuestion{{ID: "q", Noul: &NoulQuestion{}, Choice: &ChoiceQuestion{Options: []DecisionOption{{Label: "a"}}}}}},
		"duplicate ids":     {Questions: []DecisionQuestion{{ID: "q", Noul: &NoulQuestion{}}, {ID: "q", Noul: &NoulQuestion{}}}},
		"duplicate labels":  {Questions: []DecisionQuestion{{Choice: &ChoiceQuestion{Options: []DecisionOption{{Label: "a"}, {Label: "a"}}}}}},
		"empty choice":      {Questions: []DecisionQuestion{{Choice: &ChoiceQuestion{}}}},
		"too many options":  {Questions: []DecisionQuestion{{Choice: &ChoiceQuestion{Options: make([]DecisionOption, 256)}}}},
		"too many levels":   {Questions: []DecisionQuestion{{Score: &ScoreQuestion{Levels: make([]any, 11)}}}},
		"invalid state":     {State: map[string]any{"bad": make(chan int)}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := input.Validate(); err == nil {
				t.Fatal("expected invalid decision input to fail")
			}
		})
	}
}
