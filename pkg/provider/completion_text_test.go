package provider

import "testing"

func TestCompletionText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message Message
		want    string
	}{
		{"empty", Message{}, ""},
		{"legacy multiple parts", Message{Content: []Content{TextContent("first"), TextContent("second")}}, "first\n\nsecond"},
		{"message phase", Message{Phase: MessagePhaseFinalAnswer, Content: []Content{TextContent("answer")}}, "answer"},
		{"commentary only", Message{Phase: MessagePhaseCommentary, Content: []Content{TextContent("working")}}, ""},
		{"JSON commentary and answer", Message{Content: []Content{
			{Text: `{"content":"working"}`, Phase: MessagePhaseCommentary},
			{Text: `{"content":"done"}`, Phase: MessagePhaseFinalAnswer},
		}}, `{"content":"done"}`},
		{"last final answer", Message{Content: []Content{
			{Text: "draft", Phase: MessagePhaseFinalAnswer},
			{Text: "answer", Phase: MessagePhaseFinalAnswer},
		}}, "answer"},
		{"final refusal", Message{Content: []Content{
			{Text: `{"content":"draft"}`, Phase: MessagePhaseCommentary},
			{Refusal: "Cannot answer", Phase: MessagePhaseFinalAnswer},
		}}, ""},
		{"invalid final is not replaced by commentary", Message{Content: []Content{
			{Text: `{"content":"draft"}`, Phase: MessagePhaseCommentary},
			{Text: `{"content":`, Phase: MessagePhaseFinalAnswer},
		}}, `{"content":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transcript := tc.message.Text()
			completion := Completion{Message: &tc.message}
			if got := completion.Text(); got != tc.want {
				t.Fatalf("Completion.Text = %q, want %q", got, tc.want)
			}
			if tc.message.Text() != transcript {
				t.Fatal("final extraction changed the transcript")
			}
		})
	}
}

func TestCompletionTextWithoutMessage(t *testing.T) {
	if got := (Completion{}).Text(); got != "" {
		t.Fatalf("Completion.Text = %q, want empty", got)
	}
}

func TestMessageTextPreservesCommentary(t *testing.T) {
	message := Message{Phase: MessagePhaseCommentary, Content: []Content{TextContent("Working")}}
	completion := Completion{Message: &message}
	if got := message.Text(); got != "Working" {
		t.Fatalf("Message.Text = %q, want commentary", got)
	}
	if got := completion.Text(); got != "" {
		t.Fatalf("Completion.Text = %q, want no final answer", got)
	}
}

func TestCompletionTextFromStreamedPhases(t *testing.T) {
	acc := CompletionAccumulator{}
	for _, chunk := range []Message{
		{Phase: MessagePhaseCommentary},
		{Content: []Content{TextContent(`{"content":"working"}`)}},
		{Phase: MessagePhaseCommentary},
		{Content: []Content{TextContent("Still working")}},
		{Phase: MessagePhaseFinalAnswer},
		{Content: []Content{TextContent(`{"content":`)}},
		{Content: []Content{TextContent(`"done"}`)}},
	} {
		acc.Add(Completion{Message: &chunk})
	}
	completion := acc.Result()
	if got := completion.Text(); got != `{"content":"done"}` {
		t.Fatalf("Completion.Text = %q", got)
	}
	if len(completion.Message.SplitMessages()) != 3 {
		t.Fatal("lost message boundaries")
	}
}

func TestFinalMessageIndex(t *testing.T) {
	for _, tc := range []struct {
		phases []MessagePhase
		want   int
	}{
		{nil, -1},
		{[]MessagePhase{MessagePhaseCommentary}, -1},
		{[]MessagePhase{"", ""}, 1},
		{[]MessagePhase{MessagePhaseCommentary, ""}, 1},
		{[]MessagePhase{MessagePhaseFinalAnswer, ""}, 0},
		{[]MessagePhase{MessagePhaseFinalAnswer, MessagePhaseFinalAnswer}, 1},
	} {
		if got := FinalMessageIndex(tc.phases); got != tc.want {
			t.Errorf("FinalMessageIndex(%v) = %d, want %d", tc.phases, got, tc.want)
		}
	}
}
