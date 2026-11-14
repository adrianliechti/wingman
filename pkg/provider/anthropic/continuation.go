package anthropic

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func prefillUnsupported(err error) bool {
	apiError, ok := errors.AsType[*anthropic.Error](err)
	if !ok || apiError.StatusCode != 400 {
		return false
	}
	message, _ := extractAnthropicErrorInfo(apiError)
	return strings.Contains(strings.ToLower(message), "does not support assistant message prefill")
}

func canContinueAssistant(req *anthropic.BetaMessageNewParams, options *provider.CompleteOptions) bool {
	if len(req.Messages) == 0 || options.CompactionOptions != nil && options.CompactionOptions.Trigger {
		return false
	}
	last := req.Messages[len(req.Messages)-1].OfAssistant
	if last == nil || len(last.Content) == 0 {
		return false
	}
	for _, block := range last.Content {
		// These turns must resume through their native tool/compaction flow.
		if block.OfToolUse != nil || block.OfServerToolUse != nil || block.OfCompaction != nil {
			return false
		}
	}
	return true
}

func assistantContinuationMessage() anthropic.BetaMessageParam {
	return anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(provider.ContinuationText))
}

func replayAssistantContinuations(messages []anthropic.BetaMessageParam, continuations []provider.Continuation) ([]anthropic.BetaMessageParam, error) {
	for _, boundary := range continuations {
		if boundary.Message < 0 || boundary.Message >= len(messages) || boundary.Blocks <= 0 {
			return nil, errors.New("anthropic: invalid signature in thinking continuation history")
		}
		assistant := messages[boundary.Message].OfAssistant
		if assistant == nil || boundary.Blocks > len(assistant.Content) {
			return nil, errors.New("anthropic: invalid signature in thinking continuation history")
		}
		before, after := *assistant, *assistant
		before.Content = assistant.Content[:boundary.Blocks]
		after.Content = assistant.Content[boundary.Blocks:]
		replay := append([]anthropic.BetaMessageParam(nil), messages[:boundary.Message]...)
		replay = append(replay, anthropic.BetaMessageParam{OfAssistant: &before}, assistantContinuationMessage())
		if len(after.Content) > 0 {
			replay = append(replay, anthropic.BetaMessageParam{OfAssistant: &after})
		}
		messages = append(replay, messages[boundary.Message+1:]...)
	}
	return messages, nil
}
