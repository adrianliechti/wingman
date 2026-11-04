package gemini

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
)

// StreamingAccumulator manages streaming state and emits Gemini-format responses
type StreamingAccumulator struct {
	handler     StreamEventHandler
	accumulator provider.CompletionAccumulator

	responseID string
	model      string

	// Hold signatures until the next text or buffered function call establishes
	// which part carries them. Upstream often sends them in a separate chunk.
	pendingSignature string
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
	response.UsageMetadata = withTier(response.UsageMetadata, c.Tier)

	// Text and thought summaries stream as they arrive. Function calls are
	// delivered with their signatures in Complete(), once arguments are complete.
	if c.Message != nil && len(c.Message.Content) > 0 {
		var textContent []provider.Content
		for _, content := range c.Message.Content {
			if content.Reasoning != nil {
				reasoning := *content.Reasoning
				if reasoning.Signature != "" {
					s.pendingSignature = reasoning.Signature
				}
				reasoning.Signature = ""
				if reasoning.Text() != "" || reasoning.SummaryText() != "" {
					textContent = append(textContent, provider.ReasoningContent(reasoning))
				}
			}
			if content.Text != "" || content.Refusal != "" {
				if s.pendingSignature != "" {
					textContent = append(textContent, provider.ReasoningContent(provider.Reasoning{Signature: s.pendingSignature}))
					s.pendingSignature = ""
				}
				textContent = append(textContent, content)
			}
			if content.ToolCall != nil {
				// Complete reconstructs the signed call from accumulated content.
				s.pendingSignature = ""
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

	response.UsageMetadata = withTier(toUsageMetadata(result.Usage), result.Tier)

	// Determine finish reason
	finishReason := toFinishReason(result)

	// Build final candidate
	candidate := &Candidate{
		FinishReason: finishReason,
		Index:        0,
	}

	// Convert the full content before selecting calls, so each call retains
	// the signature from its preceding thought. Text and summaries were streamed.
	var parts []*Part
	if result.Message != nil {
		if content := toContent(result.Message.Content); content != nil {
			for _, part := range content.Parts {
				if part.FunctionCall != nil {
					parts = append(parts, part)
				}
			}
		}
	}
	if s.pendingSignature != "" {
		parts = append(parts, &Part{ThoughtSignature: google.DecodeThoughtSignature(s.pendingSignature)})
	}
	if len(parts) > 0 {
		candidate.Content = &Content{Role: "model", Parts: parts}
	}

	response.Candidates = []*Candidate{candidate}

	return s.handler(response)
}

// Error emits an error response in the stream
func (s *StreamingAccumulator) Error(err error) error {
	return s.handler(GenerateContentResponse{
		ResponseId:   s.responseID,
		ModelVersion: s.model,
		Error:        toAPIError(err, http.StatusInternalServerError),
	})
}

// Result returns the accumulated completion
func (s *StreamingAccumulator) Result() *provider.Completion {
	return s.accumulator.Result()
}
