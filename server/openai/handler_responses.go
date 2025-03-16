package openai

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) handleresponse(w http.ResponseWriter, r *http.Request) {
	var req ResponseRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
}

// https://platform.openai.com/docs/api-reference/responses/create
type ResponseRequest struct {
	Model string `json:"model"`

	Store  *bool `json:"store,omitempty"`
	Stream *bool `json:"stream,omitempty"`

	Instructions *string `json:"instructions,omitempty"`

	Reasoning *ResponseReasoning `json:"reasoning,omitempty"`

	// metadata

	PreviousResponseID *string `json:"previous_response_id,omitempty"`

	// text

	// tools
	// tool_choice

	TopP        *float64 `json:"top_p,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`

	Truncation *ResponseTruncation `json:"truncation,omitempty"`

	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`

	// user
}

type ResponseReasoning struct {
	Effort          string `json:"effort,omitempty"`
	GenerateSummary bool   `json:"generate_summary,omitempty"`
}

type ResponseTruncation string

const (
	ResponseTruncationAuto     ResponseTruncation = "auto"
	ResponseTruncationDisabled ResponseTruncation = "disabled"
)

// https://platform.openai.com/docs/api-reference/responses/object

type Response struct {
	ID string `json:"id"`

	Object    string `json:"object"`
	CreatedAt *int64 `json:"created_at"`

	Model string `json:"model"`

	Instructions *string `json:"instructions,omitempty"`

	Output []ResponseOutput `json:"output,omitempty"`

	// parallel_tool_calls
	PreviousResponseID *string `json:"previous_response_id,omitempty"`

	Reasoning *ResponseReasoning `json:"reasoning,omitempty"`

	Status ResponseStatus `json:"status"`

	Text any `json:"text,omitempty"`

	Tools      any `json:"tools,omitempty"`
	ToolChoice any `json:"tool_choice,omitempty"`

	TopP        *float64 `json:"top_p,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`

	Truncation *ResponseTruncation `json:"truncation,omitempty"`

	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`

	Usage *ResponseUsage `json:"usage,omitempty"`

	User     string         `json:"user,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ResponseOutput struct {
	ID string `json:"id"`

	Role string `json:"role"`

	Status ResponseOutputStatus `json:"status"`

	Type string `json:"type"`
}

type ResponseOutputType string

const (
	ResponseOutputTypeFileMessage    ResponseOutputType = "message"
	ResponseOutputTypeFileSearchCall ResponseOutputType = "file_search_call"
)

type ResponseOutputContent struct {
	Type string `json:"type"`

	Text    *string `json:"text"`
	Refusal *string `json:"refusal,omitempty"`

	// annotations
}

type ResponseOutputContentType string

const (
	ResponseOutputContentTypeText    ResponseOutputContentType = "text"
	ResponseOutputContentTypeRefusal ResponseOutputContentType = "refusal"
)

type ResponseOutputStatus string

const (
	ResponseOutputStatusInProgress ResponseOutputStatus = "in_progress"
	ResponseOutputStatusSearching  ResponseOutputStatus = "searching"
	ResponseOutputStatusCompleted  ResponseOutputStatus = "completed"
	ResponseOutputStatusIncomplete ResponseOutputStatus = "incomplete"
	ResponseOutputStatusFailed     ResponseOutputStatus = "failed"
)

type ResponseError struct {
	Code     int    `json:"code"`
	Messsage string `json:"message"`
}

type ResponseIncompleteDetails struct {
	Reason string `json:"reason"`
}

type ResponseStatus string

const (
	ResponseStatusComplete   ResponseStatus = "complete"
	ResponseStatusFailed     ResponseStatus = "failed"
	ResponseStatusInProgress ResponseStatus = "in_progress"
	ResponseStatusIncomplete ResponseStatus = "incomplete"
)

type ResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details"`
}

type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}
