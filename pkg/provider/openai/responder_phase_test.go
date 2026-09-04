package openai

import (
	"github.com/adrianliechti/wingman/pkg/provider"
	"testing"
)

func TestResponderReplaysAccumulatedMessagePhases(t *testing.T) {
	var acc provider.CompletionAccumulator
	for _, phase := range []provider.MessagePhase{provider.MessagePhaseCommentary, provider.MessagePhaseFinalAnswer} {
		acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Phase: phase, Content: []provider.Content{provider.TextContent(string(phase))}}})
	}
	responder, _ := NewResponder("https://api.openai.com/v1/", "test")
	body := responsesRequestBody(t, responder, []provider.Message{*acc.Result().Message}, &provider.CompleteOptions{})
	input := body["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("replayed %d messages, want 2", len(input))
	}
	for i, phase := range []string{"commentary", "final_answer"} {
		if input[i].(map[string]any)["phase"] != phase {
			t.Fatalf("lost phase: %+v", input)
		}
	}
}
