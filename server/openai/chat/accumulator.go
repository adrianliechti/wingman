package chat

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

// StreamEventType represents the type of streaming event
type StreamEventType string

const (
	StreamEventChunk  StreamEventType = "chunk"
	StreamEventFinish StreamEventType = "finish"
	StreamEventUsage  StreamEventType = "usage"
	StreamEventDone   StreamEventType = "done"
	StreamEventError  StreamEventType = "error"
)

// StreamEvent represents a streaming event with its data
type StreamEvent struct {
	Type StreamEventType

	// For chunk events - the chat completion chunk to emit
	Chunk *ChatCompletion

	// For error events
	Error error

	// The accumulated completion (available on finish/usage/done events)
	Completion *provider.Completion
}

// StreamEventHandler is called for each streaming event
type StreamEventHandler func(event StreamEvent) error

// StreamingAccumulator manages streaming state and emits events
type StreamingAccumulator struct {
	accumulator provider.CompletionAccumulator

	handler StreamEventHandler

	// Configuration
	model string

	// State tracking
	streamedRole bool
	finishReason FinishReason

	// Tool call tracking
	currentToolCallID string
	toolCallIndices   map[string]int
}

// NewStreamingAccumulator creates a new StreamingAccumulator with an event handler
func NewStreamingAccumulator(model string, handler StreamEventHandler) *StreamingAccumulator {
	return &StreamingAccumulator{
		handler:         handler,
		model:           model,
		finishReason:    FinishReasonStop,
		toolCallIndices: make(map[string]int),
	}
}

// Add processes a completion chunk and emits appropriate events
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	s.accumulator.Add(c)

	// Skip usage-only chunks (no content)
	if c.Usage != nil && (c.Message == nil || len(c.Message.Content) == 0) {
		return nil
	}

	chunk := &ChatCompletion{
		Object:  "chat.completion.chunk",
		ID:      c.ID,
		Model:   c.Model,
		Created: 0, // Will be set by handler
		Choices: []ChatCompletionChoice{
			{
				Delta: &ChatCompletionMessage{},
			},
		},
	}

	if chunk.Model == "" {
		chunk.Model = s.model
	}

	if c.Message != nil {
		message := &ChatCompletionMessage{}

		if content := c.Message.Text(); content != "" {
			message.Content = &content
		}

		if calls := oaiToolCalls(c.Message.Content); len(calls) > 0 {
			for i, call := range calls {
				if call.ID != "" {
					s.currentToolCallID = call.ID

					if _, found := s.toolCallIndices[s.currentToolCallID]; !found {
						s.toolCallIndices[s.currentToolCallID] = len(s.toolCallIndices)
					}
				}

				if s.currentToolCallID == "" {
					continue
				}

				calls[i] = ToolCall{
					ID:       s.currentToolCallID,
					Index:    s.toolCallIndices[s.currentToolCallID],
					Type:     call.Type,
					Function: call.Function,
				}
			}

			s.finishReason = FinishReasonToolCalls

			message.Content = nil
			message.ToolCalls = calls
		}

		chunk.Choices = []ChatCompletionChoice{
			{
				Delta: message,
			},
		}
	}

	// Add role on first chunk
	if !s.streamedRole {
		s.streamedRole = true
		chunk.Choices[0].Delta.Role = MessageRoleAssistant
	}

	return s.emitEvent(StreamEvent{
		Type:  StreamEventChunk,
		Chunk: chunk,
	})
}

// Complete signals that streaming is done and emits final events
func (s *StreamingAccumulator) Complete(includeUsage bool) error {
	result := s.accumulator.Result()

	// Emit finish chunk with reason
	if s.finishReason != "" {
		finishChunk := &ChatCompletion{
			Object:  "chat.completion.chunk",
			ID:      result.ID,
			Model:   result.Model,
			Created: 0, // Will be set by handler
			Choices: []ChatCompletionChoice{
				{
					Delta:        &ChatCompletionMessage{},
					FinishReason: &s.finishReason,
				},
			},
		}

		if finishChunk.Model == "" {
			finishChunk.Model = s.model
		}

		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventFinish,
			Chunk:      finishChunk,
			Completion: result,
		}); err != nil {
			return err
		}
	}

	// Emit usage chunk if requested and available
	if includeUsage && result.Usage != nil {
		usageChunk := &ChatCompletion{
			Object:  "chat.completion.chunk",
			ID:      result.ID,
			Model:   result.Model,
			Created: 0, // Will be set by handler
			Choices: []ChatCompletionChoice{},
			Usage: &Usage{
				PromptTokens:     result.Usage.InputTokens,
				CompletionTokens: result.Usage.OutputTokens,
				TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
			},
		}

		if usageChunk.Model == "" {
			usageChunk.Model = s.model
		}

		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventUsage,
			Chunk:      usageChunk,
			Completion: result,
		}); err != nil {
			return err
		}
	}

	// Emit done event
	return s.emitEvent(StreamEvent{
		Type:       StreamEventDone,
		Completion: result,
	})
}

// Error emits an error event
func (s *StreamingAccumulator) Error(err error) error {
	return s.emitEvent(StreamEvent{
		Type:  StreamEventError,
		Error: err,
	})
}

// Result returns the accumulated completion
func (s *StreamingAccumulator) Result() *provider.Completion {
	return s.accumulator.Result()
}

func (s *StreamingAccumulator) emitEvent(event StreamEvent) error {
	if s.handler != nil {
		return s.handler(event)
	}
	return nil
}
