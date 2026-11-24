package gemini

import (
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/internal/stream"
)

// StreamingAccumulator manages streaming state and emits Gemini-format responses
type StreamingAccumulator struct {
	handler     StreamEventHandler
	accumulator provider.CompletionAccumulator
	replay      stream.ReasoningBuffer

	responseID string
	model      string

	// python-genai records a chat turn only if every chunk has content. Keep
	// the most recent content chunk so it can also carry the finish reason.
	pending *GenerateContentResponse

	// A signature may arrive separately from the text or call it belongs to.
	reasoning provider.Reasoning

	// Once a function starts, its arguments and all following parts must be
	// emitted together in order. Earlier text has already streamed.
	afterCall *provider.CompletionAccumulator
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

	s.accumulator.Add(c)
	return s.addContent(c, s.replay.Add(c))
}

func (s *StreamingAccumulator) addContent(c provider.Completion, ordered []provider.Content) error {
	// The replay buffer already applied the snapshots of unsigned items;
	// streamed thoughts and signatures stand, as on the Messages stream.
	c.ReasoningSnapshots = nil
	if s.afterCall != nil {
		c.Message = &provider.Message{Content: ordered}
		s.afterCall.Add(c)
		return nil
	}

	var parts []*Part
	for i, content := range ordered {
		if r := content.Reasoning; r != nil {
			if s.reasoning.Signature != "" && (r.ID == "" || r.ID != s.reasoning.ID) {
				parts = append(parts, &Part{ThoughtSignature: thoughtSignature(s.reasoning)})
				s.reasoning = provider.Reasoning{}
			}
			s.reasoning.ID = r.ID
			if r.Signature != "" {
				s.reasoning.Signature = r.Signature
			}
			thought := r.Text()
			if thought == "" {
				thought = r.SummaryText()
			}
			if thought != "" {
				parts = append(parts, &Part{Text: thought, Thought: true})
			}
		}
		if text := content.Text + content.Refusal; text != "" {
			parts = append(parts, &Part{Text: text, ThoughtSignature: thoughtSignature(s.reasoning)})
			s.reasoning = provider.Reasoning{}
		}
		if content.ToolCall != nil {
			s.afterCall = &provider.CompletionAccumulator{}
			remaining := ordered[i:]
			if s.reasoning.ID != "" || s.reasoning.Signature != "" {
				remaining = append([]provider.Content{provider.ReasoningContent(s.reasoning)}, remaining...)
			}
			c.Message = &provider.Message{Content: remaining}
			s.afterCall.Add(c)
			break
		}
	}
	if len(parts) > 0 {
		if err := s.flush(); err != nil {
			return err
		}
		usage := toUsageMetadata(c.Usage)
		if usage == nil {
			usage = &UsageMetadata{}
		}
		s.pending = &GenerateContentResponse{
			ResponseId: s.responseID, ModelVersion: s.model, UsageMetadata: withTier(usage, c.Tier),
			Candidates: []*Candidate{{Content: &Content{Role: "model", Parts: parts}}},
		}
	}
	return nil
}

func (s *StreamingAccumulator) flush() error {
	if s.pending == nil {
		return nil
	}
	pending := s.pending
	s.pending = nil
	return s.handler(*pending)
}

// Complete signals that streaming is done and emits final response.
func (s *StreamingAccumulator) Complete() error {
	result := s.accumulator.Result()
	if parts := s.replay.Finish(); len(parts) > 0 {
		if err := s.addContent(provider.Completion{}, parts); err != nil {
			return err
		}
	}
	candidate := &Candidate{FinishReason: toFinishReason(result)}
	if s.afterCall != nil {
		candidate = toCandidate(s.afterCall.Result().Message.Content, candidate.FinishReason)
		if candidate.Content != nil {
			if err := s.flush(); err != nil {
				return err
			}
		}
	}
	// If every call was suppressed, the preceding content carries the finish
	// reason. An empty terminal chunk would invalidate the whole python-genai chat turn.
	if s.pending != nil {
		candidate.Content = s.pending.Candidates[0].Content
		s.pending = nil
	}
	if s.afterCall == nil && s.reasoning.Signature != "" {
		if candidate.Content == nil {
			candidate.Content = &Content{Role: "model"}
		}
		parts := candidate.Content.Parts
		signature := thoughtSignature(s.reasoning)
		if n := len(parts); s.reasoning.ID == "" && n > 0 && !parts[n-1].Thought && len(parts[n-1].ThoughtSignature) == 0 {
			parts[n-1].ThoughtSignature = signature
		} else {
			candidate.Content.Parts = append(parts, &Part{ThoughtSignature: signature})
		}
	}
	return s.handler(GenerateContentResponse{
		ResponseId: s.responseID, ModelVersion: s.model,
		Candidates: []*Candidate{candidate}, UsageMetadata: withTier(toUsageMetadata(result.Usage), result.Tier),
	})
}

// Error emits any outstanding content before the upstream error.
func (s *StreamingAccumulator) Error(err error) error {
	if parts := s.replay.Finish(); len(parts) > 0 {
		if writeErr := s.addContent(provider.Completion{}, parts); writeErr != nil {
			return writeErr
		}
	}
	if writeErr := s.flush(); writeErr != nil {
		return writeErr
	}
	// python-genai recognizes a streamed failure by its top-level error envelope.
	return s.handler(GenerateContentResponse{Error: toAPIError(err, http.StatusInternalServerError)})
}

// Result returns the accumulated completion
func (s *StreamingAccumulator) Result() *provider.Completion {
	return s.accumulator.Result()
}
