package provider

import "testing"

func TestDecisionInputValidation(t *testing.T) {
	for name, input := range map[string]*DecisionInput{
		"nil":                 nil,
		"empty questions":     {State: map[string]any{}},
		"missing variant":     {Questions: []DecisionQuestion{{ID: "q"}}},
		"multiple variants":   {Questions: []DecisionQuestion{{ID: "q", Noul: &NoulQuestion{}, Choice: &ChoiceQuestion{Options: []DecisionOption{{Label: "a"}}}}}},
		"duplicate ids":       {Questions: []DecisionQuestion{{ID: "q", Noul: &NoulQuestion{}}, {ID: "q", Noul: &NoulQuestion{}}}},
		"duplicate labels":    {Questions: []DecisionQuestion{{Choice: &ChoiceQuestion{Options: []DecisionOption{{Label: "a"}, {Label: "a"}}}}}},
		"empty choice":        {Questions: []DecisionQuestion{{Choice: &ChoiceQuestion{}}}},
		"too many options":    {Questions: []DecisionQuestion{{Choice: &ChoiceQuestion{Options: make([]DecisionOption, 256)}}}},
		"empty score levels":  {Questions: []DecisionQuestion{{Score: &ScoreQuestion{}}}},
		"too many levels":     {Questions: []DecisionQuestion{{Score: &ScoreQuestion{Levels: make([]any, 256)}}}},
		"duplicate score val": {Questions: []DecisionQuestion{{Score: &ScoreQuestion{Levels: []any{"a", "b"}, Values: []int{1, 1}}}}},
		"empty message file":  {Messages: []Message{{Role: MessageRoleUser, Content: []Content{{File: &File{}}}}}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
		"invalid state":       {State: map[string]any{"bad": make(chan int)}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
		"scalar state":        {State: true, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := input.Validate(); err == nil {
				t.Fatal("expected invalid decision input to fail")
			}
		})
	}
}

func TestDecisionScoreMetadataAndAssetsValidation(t *testing.T) {
	for _, input := range []*DecisionInput{
		{Questions: []DecisionQuestion{{Score: &ScoreQuestion{Levels: []any{"a", "b"}, Labels: []string{"a"}}}}},
		{Questions: []DecisionQuestion{{Score: &ScoreQuestion{Levels: []any{"a", "b"}, Values: []int{1}}}}},
		{Assets: map[string]*File{"photo": nil}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
		{Assets: map[string]*File{"": {Content: []byte("hi")}}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
		{Messages: []Message{{Role: "invalid"}}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
		{Messages: []Message{{Role: MessageRoleUser, Content: []Content{ToolResultContent(ToolResult{Parts: []Part{{File: &File{}}}})}}}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}},
	} {
		if err := input.Validate(); err == nil {
			t.Fatalf("accepted invalid metadata/assets: %+v", input)
		}
	}
}

func TestDecisionImageDetailValidation(t *testing.T) {
	for _, detail := range []ImageDetail{ImageDetailDefault, ImageDetailAuto, ImageDetailLow, ImageDetailHigh, ImageDetailOriginal, ImageDetail("invalid")} {
		file := &File{Content: []byte("hi"), ContentType: "image/png", ImageDetail: detail}
		input := &DecisionInput{Messages: []Message{{Role: MessageRoleUser, Content: []Content{FileContent(file)}}}, Questions: []DecisionQuestion{{Noul: &NoulQuestion{}}}}
		err := input.Validate()
		if (err == nil) != detail.Valid() {
			t.Fatalf("detail %q: %v", detail, err)
		}
	}
}
