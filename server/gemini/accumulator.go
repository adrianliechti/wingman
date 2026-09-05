package gemini

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// StreamingAccumulator manages streaming state and emits Gemini-format responses
type StreamingAccumulator struct {
	accumulator provider.CompletionAccumulator
	handler     StreamEventHandler
	responseID  string
	model       string
}

// StreamEventHandler is called for each streaming chunk
type StreamEventHandler func(response GenerateContentResponse) error

// NewStreamingAccumulator creates a new StreamingAccumulator
func NewStreamingAccumulator(responseID, model string, handler StreamEventHandler) *StreamingAccumulator {
	return &StreamingAccumulator{
		handler:    handler,
		responseID: responseID,
		model:      model,
	}
}

// Add processes a completion chunk and emits a Gemini response
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	if c.Model != "" {
		s.model = c.Model
	}

	// Build response for this chunk
	response := GenerateContentResponse{
		ResponseId:   s.responseID,
		ModelVersion: s.model,
	}

	// Gemini emits usageMetadata on every chunk; default to an empty struct so
	// the field is always present even before usage data is available.
	if u := toUsageMetadata(c.Usage); u != nil {
		response.UsageMetadata = u
	} else {
		response.UsageMetadata = &UsageMetadata{}
	}

	// Text, thoughts and thought signatures stream as they arrive. Function
	// calls are delivered once, in Complete(), so their arguments are never
	// partial.
	if c.Message != nil && len(c.Message.Content) > 0 {
		var textContent []provider.Content
		for _, content := range c.Message.Content {
			if content.Text != "" || content.Reasoning != nil {
				textContent = append(textContent, content)
			}
		}
		if len(textContent) > 0 {
			content := toContent(textContent)
			if content != nil {
				response.Candidates = []*Candidate{
					{
						Content: content,
						Index:   0,
					},
				}
			}
		}
	}

	// Gemini always includes candidates; skip chunks without content
	if len(response.Candidates) > 0 {
		if err := s.handler(response); err != nil {
			return err
		}
	}

	// Add to underlying accumulator
	s.accumulator.Add(c)

	return nil
}

// Complete signals that streaming is done and emits final response
func (s *StreamingAccumulator) Complete() error {
	result := s.accumulator.Result()

	// Build final response with finish reason
	response := GenerateContentResponse{
		ResponseId:   s.responseID,
		ModelVersion: s.model,
	}

	response.UsageMetadata = toUsageMetadata(result.Usage)

	// Determine finish reason
	finishReason := FinishReasonStop
	if result.Message != nil {
		finishReason = toFinishReason(result.Status, result.Message.Content)
	} else if result.Status == provider.CompletionStatusIncomplete {
		finishReason = FinishReasonMaxTokens
	}

	// Build final candidate
	candidate := &Candidate{
		FinishReason: finishReason,
		Index:        0,
	}

	// Only function calls go into the final chunk — text and thoughts were
	// already streamed, and clients concatenate parts across chunks.
	if result.Message != nil {
		var calls []provider.Content
		for _, c := range result.Message.Content {
			if c.ToolCall != nil {
				calls = append(calls, c)
			}
		}
		if len(calls) > 0 {
			candidate.Content = toContent(calls)
		}
	}

	response.Candidates = []*Candidate{candidate}

	return s.handler(response)
}

// Error emits an error response in the stream
func (s *StreamingAccumulator) Error(err error) error {
	code := provider.CodeFromError(err, http.StatusInternalServerError)

	var status string
	switch {
	case code == http.StatusBadRequest:
		status = "INVALID_ARGUMENT"
	case code == http.StatusUnauthorized:
		status = "UNAUTHENTICATED"
	case code == http.StatusForbidden:
		status = "PERMISSION_DENIED"
	case code == http.StatusNotFound:
		status = "NOT_FOUND"
	case code == http.StatusTooManyRequests:
		status = "RESOURCE_EXHAUSTED"
	default:
		status = "INTERNAL"

		if code >= 400 && code < 500 {
			status = "INVALID_ARGUMENT"
		}
	}

	response := GenerateContentResponse{
		ResponseId:   s.responseID,
		ModelVersion: s.model,
		Error: &APIError{
			Code:    code,
			Status:  status,
			Message: err.Error(),
		},
	}

	return s.handler(response)
}

// Result returns the accumulated completion
func (s *StreamingAccumulator) Result() *provider.Completion {
	return s.accumulator.Result()
}
