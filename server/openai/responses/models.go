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

// https://platform.openai.com/docs/api-reference/responses-streaming/response/created
type ResponseCreatedEvent struct {
	Type           string    `json:"type"` // response.created
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/in_progress
type ResponseInProgressEvent struct {
	Type           string    `json:"type"` // response.in_progress
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/completed
type ResponseCompletedEvent struct {
	Type           string    `json:"type"` // response.completed
	SequenceNumber int       `json:"sequence_number"`
	Response       *Response `json:"response"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_item/added
type OutputItemAddedEvent struct {
	Type           string      `json:"type"` // response.output_item.added
	SequenceNumber int         `json:"sequence_number"`
	OutputIndex    int         `json:"output_index"`
	Item           *OutputItem `json:"item"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_item/done
type OutputItemDoneEvent struct {
	Type           string      `json:"type"` // response.output_item.done
	SequenceNumber int         `json:"sequence_number"`
	OutputIndex    int         `json:"output_index"`
	Item           *OutputItem `json:"item"`
}

type OutputItem struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"` // message
	Status  string          `json:"status"`
	Content []OutputContent `json:"content"`
	Role    MessageRole     `json:"role,omitempty"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/content_part/added
type ContentPartAddedEvent struct {
	Type           string         `json:"type"` // response.content_part.added
	SequenceNumber int            `json:"sequence_number"`
	ItemID         string         `json:"item_id"`
	OutputIndex    int            `json:"output_index"`
	ContentIndex   int            `json:"content_index"`
	Part           *OutputContent `json:"part"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/content_part/done
type ContentPartDoneEvent struct {
	Type           string         `json:"type"` // response.content_part.done
	SequenceNumber int            `json:"sequence_number"`
	ItemID         string         `json:"item_id"`
	OutputIndex    int            `json:"output_index"`
	ContentIndex   int            `json:"content_index"`
	Part           *OutputContent `json:"part"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_text/delta
type OutputTextDeltaEvent struct {
	Type           string `json:"type"` // response.output_text.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/output_text/done
type OutputTextDoneEvent struct {
	Type           string `json:"type"` // response.output_text.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Text           string `json:"text"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/function_call_arguments/delta
type FunctionCallArgumentsDeltaEvent struct {
	Type           string `json:"type"` // response.function_call_arguments.delta
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	Delta          string `json:"delta"`
}

// https://platform.openai.com/docs/api-reference/responses-streaming/response/function_call_arguments/done
type FunctionCallArgumentsDoneEvent struct {
	Type           string `json:"type"` // response.function_call_arguments.done
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	Name           string `json:"name"`
	Arguments      string `json:"arguments"`
}

// FunctionCallOutputItem represents a function call output item
type FunctionCallOutputItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // function_call
	Status    string `json:"status"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
}

// FunctionCallOutputItemAddedEvent is emitted when a function call output item is added
type FunctionCallOutputItemAddedEvent struct {
	Type           string                  `json:"type"` // response.output_item.added
	SequenceNumber int                     `json:"sequence_number"`
	OutputIndex    int                     `json:"output_index"`
	Item           *FunctionCallOutputItem `json:"item"`
}

// FunctionCallOutputItemDoneEvent is emitted when a function call output item is done
type FunctionCallOutputItemDoneEvent struct {
	Type           string                  `json:"type"` // response.output_item.done
	SequenceNumber int                     `json:"sequence_number"`
	OutputIndex    int                     `json:"output_index"`
	Item           *FunctionCallOutputItem `json:"item"`
}
