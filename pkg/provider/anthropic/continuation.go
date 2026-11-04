package anthropic

import (
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

const assistantContinuationText = "Continue the previous response without repeating completed work."
const continuationSignaturePrefix = "wm:claude-continuation:"

// The inserted user turn must be reconstructed on later requests: thinking
// signatures bind to the native conversation, including this adapter's turns.
// Carry the boundaries in opaque reasoning state, leaving client history alone.
type assistantContinuation struct {
	Message int `json:"m"`
	Blocks  int `json:"b"`
}

type continuationSignature struct {
	Signature     string                  `json:"s"`
	Continuations []assistantContinuation `json:"c"`
}

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
	return anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(assistantContinuationText))
}

func wrapContinuationSignature(signature string, continuations []assistantContinuation) string {
	if signature == "" || len(continuations) == 0 {
		return signature
	}
	realm, raw := provider.ParseSignature(signature)
	data, _ := json.Marshal(continuationSignature{Signature: raw, Continuations: continuations})
	return provider.TagSignature(realm, continuationSignaturePrefix+base64.RawURLEncoding.EncodeToString(data))
}

func unwrapContinuationSignature(value string) (string, []assistantContinuation) {
	realm, raw := provider.ParseSignature(value)
	encoded, ok := strings.CutPrefix(raw, continuationSignaturePrefix)
	if !ok {
		return value, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return value, nil
	}
	var state continuationSignature
	if json.Unmarshal(data, &state) != nil || state.Signature == "" || len(state.Continuations) == 0 {
		return value, nil
	}
	return provider.TagSignature(realm, state.Signature), state.Continuations
}

func assistantContinuations(input []provider.Message) []assistantContinuation {
	var result []assistantContinuation
	for _, message := range input {
		for _, content := range message.Content {
			if content.Reasoning != nil {
				_, continuations := unwrapContinuationSignature(content.Reasoning.Signature)
				if len(continuations) > len(result) {
					result = continuations
				}
			}
		}
	}
	return result
}

func replayAssistantContinuations(messages []anthropic.BetaMessageParam, continuations []assistantContinuation) ([]anthropic.BetaMessageParam, error) {
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
