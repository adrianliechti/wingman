package openai

import (
	"fmt"
	"github.com/adrianliechti/wingman/pkg/provider"
	"testing"
)

func TestResponderReplaysAccumulatedMessagePhases(t *testing.T) {
	for _, phases := range [][]provider.MessagePhase{
		{provider.MessagePhaseCommentary, provider.MessagePhaseFinalAnswer},
		{provider.MessagePhaseCommentary, ""},
		{"", provider.MessagePhaseFinalAnswer},
		{"", ""},
	} {
		t.Run(fmt.Sprint(phases), func(t *testing.T) {
			var acc provider.CompletionAccumulator
			for i, phase := range phases {
				acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{{MessageID: fmt.Sprintf("msg_%d", i), Phase: phase, Text: fmt.Sprintf("message %d", i)}}}})
			}
			responder, _ := NewResponder("https://api.openai.com/v1/", "test")
			body := responsesRequestBody(t, responder, []provider.Message{*acc.Result().Message}, &provider.CompleteOptions{})
			input := body["input"].([]any)
			if len(input) != 2 {
				t.Fatalf("replayed %d messages, want 2", len(input))
			}
			for i, phase := range phases {
				value, _ := input[i].(map[string]any)["phase"].(string)
				if value != string(phase) {
					t.Fatalf("lost phase: %+v", input)
				}
			}
		})
	}
}
