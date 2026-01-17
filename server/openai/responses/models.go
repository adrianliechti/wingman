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

	Tools []Tool `json:"tools,omitempty"`

	Text *TextConfig `json:"text,omitempty"`

	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
	Temperature     *float32 `json:"temperature,omitempty"`

	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`

	//ToolChoice        any  `json:"tool_choice,omitempty"`
	//ParallelToolCalls bool `json:"parallel_tool_calls,omitempty"`
}

// ReasoningConfig contains configuration options for reasoning models
type ReasoningConfig struct {
	Effort *ReasoningEffort `json:"effort,omitempty"`
}

type ReasoningEffort string

var (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
)

// TextConfig represents configuration options for text responses
type TextConfig struct {
	Format    *TextFormat `json:"format,omitempty"`
	Verbosity *Verbosity  `json:"verbosity,omitempty"`
}

type Verbosity string

var (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

// TextFormat represents the format configuration for text output
type TextFormat struct {
	Type string `json:"type,omitempty"` // "text", "json_object", or "json_schema"

	// For json_schema type
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

// ToolType represents the type of tool
type ToolType string

const (
	ToolTypeFunction ToolType = "function"
	ToolTypeCustom   ToolType = "custom"
)

// Tool represents a tool in the request
type Tool struct {
	Type ToolType `json:"type"`

	// For function tools
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`

	// For custom tools (like apply_patch)
	Format *CustomToolFormat `json:"format,omitempty"`
}

// CustomToolFormat describes the format for custom tools
type CustomToolFormat struct {
	Type       string `json:"type,omitempty"`       // "grammar"
	Syntax     string `json:"syntax,omitempty"`     // "lark"
	Definition string `json:"definition,omitempty"` // the grammar definition
}

// InputItemType represents the type of input item
type InputItemType string

const (
	InputItemTypeMessage            InputItemType = "message"
	InputItemTypeReasoning          InputItemType = "reasoning"
	InputItemTypeFunctionCall       InputItemType = "function_call"
	InputItemTypeFunctionCallOutput InputItemType = "function_call_output"
)

type ResponsesInput struct {
	Items []InputItem `json:"-"`
}

// InputItem represents a single item in the input array
type InputItem struct {
	Type InputItemType `json:"type,omitempty"`

	// For message type
	*InputMessage

	// For reasoning type
	*InputReasoning

	// For function_call type
	*InputFunctionCall

	// For function_call_output type
	*InputFunctionCallOutput
}

// InputReasoning represents a reasoning item in the input
type InputReasoning struct {
	ID               string                 `json:"id,omitempty"`
	Summary          []ReasoningSummaryPart `json:"summary,omitempty"`
	Content          json.RawMessage        `json:"content,omitempty"`           // Can be null
	EncryptedContent string                 `json:"encrypted_content,omitempty"` // Base64 encoded
}

// ReasoningSummaryPart represents a part of the reasoning summary
type ReasoningSummaryPart struct {
	Type string `json:"type,omitempty"` // "summary_text"
	Text string `json:"text,omitempty"`
}

// InputFunctionCall represents a function call in the input
type InputFunctionCall struct {
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Status    string `json:"status,omitempty"` // "in_progress", "completed"
}

// InputFunctionCallOutput represents a function call output in the input
type InputFunctionCallOutput struct {
	CallID string `json:"call_id,omitempty"`
	Output string `json:"output,omitempty"`
}

func (ri *ResponsesInput) UnmarshalJSON(data []byte) error {
	// Try string input first
	var stringInput string
	if err := json.Unmarshal(data, &stringInput); err == nil {
		ri.Items = []InputItem{
			{
				Type: InputItemTypeMessage,
				InputMessage: &InputMessage{
					Role: MessageRoleUser,
					Content: []InputContent{
						{
							Type: InputContentText,
							Text: stringInput,
						},
					},
				},
			},
		}
		return nil
	}

	// Try array of input items
	var rawItems []json.RawMessage
	if err := json.Unmarshal(data, &rawItems); err != nil {
		return errors.New("failed to unmarshal ResponsesInput")
	}

	ri.Items = make([]InputItem, 0, len(rawItems))

	for _, raw := range rawItems {
		// First, determine the type
		var typeWrapper struct {
			Type InputItemType `json:"type"`
		}
		if err := json.Unmarshal(raw, &typeWrapper); err != nil {
			return err
		}

		item := InputItem{Type: typeWrapper.Type}

		switch typeWrapper.Type {
		case InputItemTypeMessage, "":
			// Default to message type for backwards compatibility
			var msg InputMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				return err
			}
			item.Type = InputItemTypeMessage
			item.InputMessage = &msg

		case InputItemTypeReasoning:
			var reasoning InputReasoning
			if err := json.Unmarshal(raw, &reasoning); err != nil {
				return err
			}
			item.InputReasoning = &reasoning

		case InputItemTypeFunctionCall:
			var fc InputFunctionCall
			if err := json.Unmarshal(raw, &fc); err != nil {
				return err
			}
			item.InputFunctionCall = &fc

		case InputItemTypeFunctionCallOutput:
			var fco InputFunctionCallOutput
			if err := json.Unmarshal(raw, &fco); err != nil {
				return err
			}
			item.InputFunctionCallOutput = &fco

		default:
			return fmt.Errorf("unknown input item type: %s", typeWrapper.Type)
		}

		ri.Items = append(ri.Items, item)
	}

	return nil
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
	Type InputContentType `json:"type,omitempty"`
	Text string           `json:"text,omitempty"`

	ImageURL string `json:"image_url,omitempty"`

	Filename string `json:"filename,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
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

	Status string `json:"status,omitempty"` // completed, failed, in_progress, incomplete

	Output []ResponseOutput `json:"output"`

	Usage *Usage `json:"usage,omitempty"`

	Error *ResponseError `json:"error,omitempty"`
}

// Usage contains token usage information
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ResponseError contains error details when a response fails
type ResponseError struct {
	Code    string `json:"code"`    // e.g., "server_error", "rate_limit_exceeded"
	Message string `json:"message"` // Human-readable error message
}

type ResponseOutput struct {
	Type ResponseOutputType `json:"type,omitempty"`

	*OutputMessage
	*FunctionCallOutputItem
}

type ResponseOutputType string

var (
	ResponseOutputTypeMessage      ResponseOutputType = "message"
	ResponseOutputTypeFunctionCall ResponseOutputType = "function_call"
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

// https://platform.openai.com/docs/api-reference/responses-streaming/response/failed
type ResponseFailedEvent struct {
	Type           string    `json:"type"` // response.failed
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
	Name           string `json:"name"`
	OutputIndex    int    `json:"output_index"`
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
