package provider

import (
	"encoding/base64"
	"encoding/json/v2"
	"strings"
)

// Appended as a user turn when a conversation ends with an assistant turn the model will not continue.
const ContinuationText = "Continue the previous response without repeating completed work."

// EndsWithAssistantTurn reports whether the conversation's last turn is assistant output with no pending tool call.
func EndsWithAssistantTurn(messages []Message) bool {
	output := false
	// One assistant turn can span several messages, so a call anywhere in the trailing run is still pending.
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role == MessageRoleSystem {
			continue
		}
		if message.Role != MessageRoleAssistant {
			break
		}
		for _, content := range message.Content {
			if content.ToolCall != nil {
				return false
			}
			output = output || content.Text != "" || content.Reasoning != nil
		}
	}
	return output
}

const continuationSignaturePrefix = "wm:claude-continuation:"

// Continuation marks where an adapter inserted a user turn into the native conversation.
type Continuation struct {
	Message int `json:"m"`
	Blocks  int `json:"b"`
}

type continuationSignature struct {
	Signature     string         `json:"s"`
	Continuations []Continuation `json:"c"`
}

// Thinking signatures bind to the native conversation, so the inserted turns travel inside them and are replayed later.
func WrapContinuationSignature(signature string, continuations []Continuation) string {
	if signature == "" || len(continuations) == 0 {
		return signature
	}
	realm, raw := ParseSignature(signature)
	data, _ := json.Marshal(continuationSignature{Signature: raw, Continuations: continuations})
	return TagSignature(realm, continuationSignaturePrefix+base64.RawURLEncoding.EncodeToString(data))
}

func UnwrapContinuationSignature(value string) (string, []Continuation) {
	realm, raw := ParseSignature(value)
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
	return TagSignature(realm, state.Signature), state.Continuations
}

// Continuations returns the inserted turns recorded in the conversation's reasoning signatures.
func Continuations(input []Message) []Continuation {
	var result []Continuation
	for _, message := range input {
		for _, content := range message.Content {
			if content.Reasoning != nil {
				_, continuations := UnwrapContinuationSignature(content.Reasoning.Signature)
				if len(continuations) > len(result) {
					result = continuations
				}
			}
		}
	}
	return result
}
