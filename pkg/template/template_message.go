package template

import (
	"slices"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func Message(message provider.Message, data any) (provider.Message, error) {
	t, err := NewTemplate(message.Content.Text())

	if err != nil {
		return message, err
	}

	content, err := t.Execute(data)

	if err != nil {
		return message, err
	}

	message.Content = provider.MessageContent{
		{
			Text: content,
		},
	}

	return message, nil
}

func Messages(messages []provider.Message, data any) ([]provider.Message, error) {
	result := slices.Clone(messages)

	for i, m := range result {
		message, err := Message(m, data)

		if err != nil {
			return nil, err
		}

		result[i] = message
	}

	return result, nil
}
