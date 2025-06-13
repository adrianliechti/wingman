package openai

import (
	"encoding/json"
	"errors"
)

// https://platform.openai.com/docs/api-reference/responses/create
type ResponseRequest struct {
	Model string `json:"model"`

	Input ResponseInput `json:"input"`

	Instructions *string `json:"instructions"`

	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`

	Stream      *bool    `json:"stream,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
}

type ResponseInput struct {
	Messages []ResponseMessage `json:"-"`
}

func (r *ResponseInput) UnmarshalJSON(data []byte) error {
	var text string

	if err := json.Unmarshal(data, &text); err == nil {
		*r = ResponseInput{
			Messages: []ResponseMessage{
				{
					Type: "input_text",
					Role: "user",

					Content: ResponseInputContent{
						{
							Type: "input_text",

							ResponseInputText: &ResponseInputText{
								Text: &text,
							},
						},
					},
				},
			},
		}

		return nil
	}

	var messages []ResponseMessage

	if err := json.Unmarshal(data, &messages); err == nil {
		*r = ResponseInput{
			Messages: messages,
		}

		return nil

	}

	return nil
}

func (r *ResponseInput) MarshalJSON() ([]byte, error) {
	if len(r.Messages) > 0 {
		return json.Marshal(r.Messages)
	}

	return nil, errors.New("no messages to marshal")
}

type ResponseMessage struct {
	ID string `json:"id,omitempty"`

	Type   string `json:"type"`
	Status string `json:"status,omitempty"` // completed, failed, in_progress, cancelled, queued, or incomplete

	Role MessageRole `json:"role"`

	Content ResponseInputContent `json:"content,omitempty"`
}

type ResponseInputContent []struct {
	Type string `json:"type,omitempty"`

	*ResponseInputText
	*ResponseInputFile
	*ResponseInputImage
}

func (c *ResponseInputContent) UnmarshalJSON(data []byte) error {
	var text string

	if err := json.Unmarshal(data, &text); err == nil {
		*c = ResponseInputContent{
			{
				Type: "input_text",

				ResponseInputText: &ResponseInputText{
					Text: &text,
				},
			},
		}

		return nil
	}

	var content []struct {
		Type string `json:"type,omitempty"`

		*ResponseInputText
		*ResponseInputFile
		*ResponseInputImage
	}

	if err := json.Unmarshal(data, &content); err == nil {
		*c = content
		return nil
	}

	return nil
}

type ResponseInputText struct {
	Text        *string `json:"text,omitempty"`
	Annotations []any   `json:"annotations,omitempty"`
}

type ResponseInputFile struct {
	FileID   *string `json:"file_id,omitempty"`
	FileData *string `json:"file_data,omitempty"`

	FileName *string `json:"filename,omitempty"`
}

type ResponseInputImage struct {
	Detail   *string `json:"detail,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}

type Response struct {
	ID string `json:"id"`

	Object string `json:"object"` // "response"

	CreatedAt int64  `json:"created_at"`
	Status    string `json:"status"` // "completed", "failed", "cancelled"

	Model string `json:"model"`

	Output ResponseOutput `json:"output"`
}

type ResponseOutput struct {
	Message *ResponseMessage `json:"message,omitempty"`
}

func (o *ResponseOutput) MarshalJSON() ([]byte, error) {
	if o.Message != nil {
		return json.Marshal(o.Message)
	}

	return nil, errors.New("no output to marshal")
}
