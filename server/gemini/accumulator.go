package gemini

import (
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

	// Add usage metadata if available
	if c.Usage != nil {
		response.UsageMetadata = &UsageMetadata{
			PromptTokenCount:     c.Usage.InputTokens,
			CandidatesTokenCount: c.Usage.OutputTokens,
			TotalTokenCount:      c.Usage.InputTokens + c.Usage.OutputTokens,
		}
	}

	// Convert content to Gemini format
	if c.Message != nil && len(c.Message.Content) > 0 {
		content := toContent(c.Message.Content)
		if content != nil {
			response.Candidates = []*Candidate{
				{
					Content: content,
					Index:   0,
				},
			}
		}
	}

	// Emit the response
	if err := s.handler(response); err != nil {
		return err
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

	// Add final usage metadata
	if result.Usage != nil {
		response.UsageMetadata = &UsageMetadata{
			PromptTokenCount:     result.Usage.InputTokens,
			CandidatesTokenCount: result.Usage.OutputTokens,
			TotalTokenCount:      result.Usage.InputTokens + result.Usage.OutputTokens,
		}
	}

	// Determine finish reason
	finishReason := FinishReasonStop
	if result.Message != nil {
		finishReason = toFinishReason(result.Message.Content)
	}

	// Build final candidate
	candidate := &Candidate{
		FinishReason: finishReason,
		Index:        0,
	}

	// Only include function calls in final response (text was already streamed)
	// The SDK expects to find function calls in the last response
	if result.Message != nil && len(result.Message.Content) > 0 {
		var hasFunctionCalls bool
		for _, c := range result.Message.Content {
			if c.ToolCall != nil {
				hasFunctionCalls = true
				break
			}
		}
		if hasFunctionCalls {
			candidate.Content = toContent(result.Message.Content)
		}
	}

	response.Candidates = []*Candidate{candidate}

	return s.handler(response)
}

// Error emits an error response
func (s *StreamingAccumulator) Error(err error) error {
	// For streaming, we don't have a good way to signal errors in Gemini format
	// The connection will be closed and the client should handle it
	return nil
}

// Result returns the accumulated completion
func (s *StreamingAccumulator) Result() *provider.Completion {
	return s.accumulator.Result()
}
