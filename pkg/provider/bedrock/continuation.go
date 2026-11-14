package bedrock

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func prefillUnsupported(err error) bool {
	validation, ok := errors.AsType[*types.ValidationException](err)
	return ok && strings.Contains(strings.ToLower(aws.ToString(validation.Message)), "does not support assistant message prefill")
}

func canContinueAssistant(messages []types.Message) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	if last.Role != types.ConversationRoleAssistant || len(last.Content) == 0 {
		return false
	}
	for _, block := range last.Content {
		// A pending tool call resumes through its tool result, not through a new user turn.
		if _, pending := block.(*types.ContentBlockMemberToolUse); pending {
			return false
		}
	}
	return true
}

func assistantContinuationMessage() types.Message {
	return types.Message{
		Role:    types.ConversationRoleUser,
		Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: provider.ContinuationText}},
	}
}

// Mirrors the Anthropic adapter: a signature produced after an inserted turn only verifies with that turn in place.
func replayAssistantContinuations(messages []types.Message, continuations []provider.Continuation) ([]types.Message, error) {
	for _, boundary := range continuations {
		if boundary.Message < 0 || boundary.Message >= len(messages) || boundary.Blocks <= 0 {
			return nil, errors.New("bedrock: invalid signature in thinking continuation history")
		}
		assistant := messages[boundary.Message]
		if assistant.Role != types.ConversationRoleAssistant || boundary.Blocks > len(assistant.Content) {
			return nil, errors.New("bedrock: invalid signature in thinking continuation history")
		}
		before, after := assistant, assistant
		before.Content = assistant.Content[:boundary.Blocks]
		after.Content = assistant.Content[boundary.Blocks:]
		replay := append([]types.Message(nil), messages[:boundary.Message]...)
		replay = append(replay, before, assistantContinuationMessage())
		if len(after.Content) > 0 {
			replay = append(replay, after)
		}
		messages = append(replay, messages[boundary.Message+1:]...)
	}
	return messages, nil
}
