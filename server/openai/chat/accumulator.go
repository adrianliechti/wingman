package chat

import (
	"slices"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
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
	id    string
	model string

	// State tracking
	streamedRole bool
	finishReason FinishReason

	// Tool call tracking
	currentToolCallID string
	toolCallIndices   map[string]int
	toolCallArgsSeen  map[string]bool
}

// NewStreamingAccumulator creates a new StreamingAccumulator with an event handler
func NewStreamingAccumulator(model string, handler StreamEventHandler) *StreamingAccumulator {
	return &StreamingAccumulator{
		handler:          handler,
		id:               "chatcmpl-" + uuid.NewString(),
		model:            model,
		finishReason:     FinishReasonStop,
		toolCallIndices:  make(map[string]int),
		toolCallArgsSeen: make(map[string]bool),
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
		ServiceTier: "default",
	}

	if chunk.ID == "" {
		chunk.ID = s.id
	}

	if chunk.Model == "" {
		chunk.Model = s.model
	}

	if c.Message != nil {
		message := &ChatCompletionMessage{}

		if content := c.Message.Text(); content != "" {
			message.Content = &content
		}

		if refusal := c.Message.Refusal(); refusal != "" {
			message.Refusal = &refusal
		}

		if calls := oaiToolCalls(c.Message.Content); len(calls) > 0 {
			// OpenAI streams id/type/name only on a call's first chunk;
			// clients accumulate name and arguments with += keyed by index,
			// so repeating them would corrupt client state.
			chunks := make([]ToolCall, 0, len(calls))

			for _, call := range calls {
				first := false

				if call.ID != "" {
					s.currentToolCallID = call.ID

					if _, found := s.toolCallIndices[call.ID]; !found {
						s.toolCallIndices[call.ID] = len(s.toolCallIndices)
						first = true
					}
				}

				if s.currentToolCallID == "" {
					continue
				}

				if call.Function != nil && call.Function.Arguments != "" {
					s.toolCallArgsSeen[s.currentToolCallID] = true
				}

				idx := s.toolCallIndices[s.currentToolCallID]

				chunk := ToolCall{
					Index:    &idx,
					Function: &FunctionCall{},
				}

				if call.Function != nil {
					chunk.Function.Arguments = call.Function.Arguments
				}

				if first {
					chunk.ID = s.currentToolCallID
					chunk.Type = call.Type

					if call.Function != nil {
						chunk.Function.Name = call.Function.Name
					}
				}

				chunks = append(chunks, chunk)
			}

			if len(chunks) > 0 {
				s.finishReason = FinishReasonToolCalls
				message.ToolCalls = chunks
			}
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

	// Truncation and content-filter override any earlier tool_calls reason —
	// the upstream finish_reason flows through Completion.Status.
	switch result.Status {
	case provider.CompletionStatusIncomplete:
		s.finishReason = FinishReasonLength
	case provider.CompletionStatusRefused:
		s.finishReason = FinishReasonContentFilter
	}

	// Calls that streamed no argument bytes must still deliver parseable
	// arguments — clients rebuild them purely from deltas.
	if s.finishReason == FinishReasonToolCalls {
		for _, id := range s.pendingArgumentIDs() {
			idx := s.toolCallIndices[id]

			chunk := &ChatCompletion{
				Object:  "chat.completion.chunk",
				ID:      result.ID,
				Model:   result.Model,
				Created: 0, // Will be set by handler
				Choices: []ChatCompletionChoice{
					{
						Delta: &ChatCompletionMessage{
							ToolCalls: []ToolCall{
								{
									Index:    &idx,
									Function: &FunctionCall{Arguments: "{}"},
								},
							},
						},
					},
				},
				ServiceTier: "default",
			}

			if chunk.ID == "" {
				chunk.ID = s.id
			}

			if chunk.Model == "" {
				chunk.Model = s.model
			}

			if err := s.emitEvent(StreamEvent{
				Type:  StreamEventChunk,
				Chunk: chunk,
			}); err != nil {
				return err
			}
		}
	}

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

		if finishChunk.ID == "" {
			finishChunk.ID = s.id
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
				PromptTokensDetails: &PromptTokensDetails{
					CachedTokens: result.Usage.CacheReadInputTokens,
				},
				CompletionTokensDetails: &CompletionTokensDetails{
					ReasoningTokens: result.Usage.ReasoningTokens,
				},
			},
		}

		if usageChunk.ID == "" {
			usageChunk.ID = s.id
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

// pendingArgumentIDs returns tool call IDs without streamed arguments,
// ordered by their emitted index.
func (s *StreamingAccumulator) pendingArgumentIDs() []string {
	ids := make([]string, 0, len(s.toolCallIndices))

	for id := range s.toolCallIndices {
		if !s.toolCallArgsSeen[id] {
			ids = append(ids, id)
		}
	}

	slices.SortFunc(ids, func(a, b string) int {
		return s.toolCallIndices[a] - s.toolCallIndices[b]
	})

	return ids
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
