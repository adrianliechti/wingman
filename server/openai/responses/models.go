package responses

import (
	"encoding/json"
	"errors"
	"fmt"
)

// https://platform.openai.com/docs/api-reference/responses/create
type ResponsesRequest struct {
	Model string `json:"model,omitempty"`

	Stream bool `json:"stream,omitempty"`

	Instructions string `json:"instructions,omitempty"`

	Input ResponsesInput `json:"input"`
}

type ResponsesInput struct {
	Messages []InputMessage `json:"-"`
}

func (ri *ResponsesInput) UnmarshalJSON(data []byte) error {
	var stringInput string

	if err := json.Unmarshal(data, &stringInput); err == nil {
		ri.Messages = []InputMessage{
			{
				Role: MessageRoleUser,
				Content: []InputContent{
					{
						Type: InputContentText,
						Text: stringInput,
					},
				},
			},
		}

		return nil
	}

	var messages []InputMessage

	if err := json.Unmarshal(data, &messages); err == nil {
		ri.Messages = messages

		return nil
	}

	return errors.New("failed to unmarshal ResponsesInput")
}

type MessageRole string

var (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
	MessageRoleDeveloper MessageRole = "developer"
)

type InputMessage struct {
	Role MessageRole `json:"role,omitempty"`

	Content []InputContent `json:"content,omitempty"`
}

func (im *InputMessage) UnmarshalJSON(data []byte) error {
	var stringInput string

	if err := json.Unmarshal(data, &stringInput); err == nil {
		im.Role = MessageRoleUser

		im.Content = []InputContent{
			{
				Type: InputContentText,
				Text: stringInput,
			},
		}

		return nil
	}

	var textInput struct {
		Role MessageRole `json:"role"`
		Text string      `json:"text"`
	}

	if err := json.Unmarshal(data, &textInput); err == nil && textInput.Role != "" && textInput.Text != "" {
		im.Role = textInput.Role

		im.Content = []InputContent{
			{
				Type: InputContentText,
				Text: textInput.Text,
			},
		}

		return nil
	}

	var messageInput struct {
		Role    MessageRole `json:"role"`
		Content any         `json:"content"`
	}

	if err := json.Unmarshal(data, &messageInput); err == nil {
		im.Role = messageInput.Role

		switch content := messageInput.Content.(type) {
		case string:
			im.Content = []InputContent{
				{
					Type: InputContentText,
					Text: content,
				},
			}

			return nil

		case []any:
			data, err := json.Marshal(content)

			if err != nil {
				return err
			}

			if err := json.Unmarshal(data, &im.Content); err != nil {
				return err
			}

			return nil

		default:
			return fmt.Errorf("unsupported content type: %T", content)
		}
	}

	return errors.New("failed to unmarshal InputMessage")
}

type InputContent struct {
	Type     InputContentType `json:"type,omitempty"`
	Text     string           `json:"text,omitempty"`
	ImageURL string           `json:"image_url,omitempty"`
}

func (ic *InputContent) UnmarshalJSON(data []byte) error {
	// Define a temporary struct to avoid recursion
	type Alias InputContent
	alias := &Alias{}

	if err := json.Unmarshal(data, alias); err != nil {
		return err
	}

	*ic = InputContent(*alias)
	return nil
}

type InputContentType string

const (
	InputContentText  InputContentType = "input_text"
	InputContentImage InputContentType = "input_image"
	InputContentFile  InputContentType = "input_file"
)

type Response struct {
	ID string `json:"id,omitempty"`

	Object string `json:"object,omitempty"` // response

	CreatedAt int64 `json:"created_at"`

	Model string `json:"model,omitempty"`

	Status string `json:"status,omitempty"` // completed

	Output []ResponseOutput `json:"output,omitempty"`
}

type ResponseOutput struct {
	Type ResponseOutputType `json:"type,omitempty"`

	*OutputMessage
}

type ResponseOutputType string

var (
	ResponseOutputTypeMessage ResponseOutputType = "message"
)

type OutputMessage struct {
	ID string `json:"id,omitempty"`

	Role MessageRole `json:"role,omitempty"`

	Status string `json:"status,omitempty"` // completed

	Contents []OutputContent `json:"content,omitempty"`
}

type OutputContent struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}
