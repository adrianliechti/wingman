package google

import (
	"errors"
	"os"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/joho/godotenv"
)

func TestComplete_LiveUnsignedToolReplay(t *testing.T) {
	if os.Getenv("WINGMAN_GOOGLE_LIVE") != "1" {
		t.Skip("set WINGMAN_GOOGLE_LIVE=1 to test unsigned history")
	}
	values, _ := godotenv.Read("../../../.env")
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		key = values["GEMINI_API_KEY"]
	}
	if key == "" {
		t.Fatal("GEMINI_API_KEY is required")
	}
	c, err := NewCompleter("gemini-4-argon", WithToken(key))
	if err != nil {
		t.Fatal(err)
	}
	for _, signature := range []string{"", "invalid_signature"} {
		messages := []provider.Message{provider.UserMessage("Call lookup then reply with DONE."), {Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Signature: signature}), provider.ToolCallContent(provider.ToolCall{ID: "call_foreign", Name: "lookup", Arguments: `{}`})}}, provider.ToolMessage("call_foreign", "success")}
		var failure error
		options := &provider.CompleteOptions{MaxTokens: new(128), Tools: []provider.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}, ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceNone}}
		if signature == "" {
			result := liveComplete(t, c, messages, options)
			liveWantText(t, result.Message.Text(), "DONE")
			continue
		}
		for _, err := range c.Complete(t.Context(), messages, options) {
			if err != nil {
				failure = err
				break
			}
		}
		var apierr *provider.ProviderError
		if !errors.As(failure, &apierr) || apierr.Code != 400 {
			t.Fatalf("invalid existing signature must still fail: %v", failure)
		}
	}
	t.Run("text before thought summary", func(t *testing.T) {
		messages := []provider.Message{
			provider.UserMessage("Call lookup then reply with DONE."),
			provider.AssistantMessage("Checking history."),
			{Role: provider.MessageRoleAssistant, Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("Run the lookup tool.")}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_foreign", Name: "lookup", Arguments: `{}`}),
			}},
			provider.ToolMessage("call_foreign", "success"),
		}
		result := liveComplete(t, c, messages, &provider.CompleteOptions{
			MaxTokens: new(128), Tools: []provider.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
			ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceNone}, ReasoningOptions: &provider.ReasoningOptions{IncludeSummary: true},
		})
		liveWantText(t, result.Message.Text(), "DONE")
	})
}
