package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type message struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
}

func parseMessages(messages []message) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	for _, m := range messages {
		message, err := parseMessage(m)

		if err != nil {
			return nil, err

		}

		result = append(result, *message)
	}

	return result, nil
}

func parseMessage(message message) (*provider.Message, error) {
	var role provider.MessageRole

	if strings.EqualFold(message.Role, string(provider.MessageRoleSystem)) {
		role = provider.MessageRoleSystem
	}

	if strings.EqualFold(message.Role, string(provider.MessageRoleUser)) {
		role = provider.MessageRoleUser
	}

	if strings.EqualFold(message.Role, string(provider.MessageRoleAssistant)) {
		role = provider.MessageRoleAssistant
	}

	if role == "" {
		return nil, errors.New("invalid message role: " + message.Role)
	}

	return &provider.Message{
		Role: role,

		Content: provider.MessageContent{
			provider.TextContent(message.Content),
		},
	}, nil
}
