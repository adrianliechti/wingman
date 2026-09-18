package bedrock

import (
	"errors"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestReplaySignedThinkingFromResponsesSummary(t *testing.T) {
	for _, tc := range []struct {
		name, text, summary, want string
	}{
		{"summary replay", "", "  signed thinking\n\n", "  signed thinking\n\n"},
		{"original text wins", "original\n", "display summary", "original\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := convertAssistantContent(provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{
					Text: tc.text, Summary: tc.summary, Signature: "signature",
				})},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(content) != 1 {
				t.Fatalf("content = %+v", content)
			}
			block := content[0].(*types.ContentBlockMemberReasoningContent).Value.(*types.ReasoningContentBlockMemberReasoningText).Value
			if aws.ToString(block.Text) != tc.want || aws.ToString(block.Signature) != "signature" {
				t.Fatalf("signed reasoning changed: %+v", block)
			}
		})
	}
}

func TestConverseRejectsUnsupportedReasoningContext(t *testing.T) {
	c := &Completer{Config: &Config{model: "anthropic.claude-sonnet-4-6-v1:0"}}
	for _, mode := range []provider.ReasoningContext{"", provider.ReasoningContextAuto, provider.ReasoningContextCurrentTurn, provider.ReasoningContextAllTurns} {
		t.Run(string(mode), func(t *testing.T) {
			_, err := c.convertConverseInput([]provider.Message{provider.UserMessage("Hello")}, &provider.CompleteOptions{
				ReasoningOptions: &provider.ReasoningOptions{Context: mode},
			})
			if mode == "" || mode == provider.ReasoningContextAuto {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var providerErr *provider.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Code != 400 || providerErr.Type != "invalid_request_error" {
				t.Fatalf("error = %v, want invalid_request_error", err)
			}
		})
	}
}
