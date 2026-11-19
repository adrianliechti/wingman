package chat

import (
	"slices"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai/shared"

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
	handler     StreamEventHandler
	accumulator provider.CompletionAccumulator

	// Configuration
	id    string
	model string

	// State tracking
	streamedRole bool
	tier         provider.Tier

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
		toolCallIndices:  make(map[string]int),
		toolCallArgsSeen: make(map[string]bool),
	}
}

// Add processes a completion chunk and emits appropriate events
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	s.accumulator.Add(c)
	if c.Tier != "" {
		s.tier = c.Tier
	}
	// Metadata can be absent from later deltas, or arrive after output starts.
	// Once a chunk is visible, its identity must remain stable through usage.
	if !s.streamedRole {
		if c.ID != "" {
			s.id = c.ID
		}
		if c.Model != "" {
			s.model = c.Model
		}
	}

	// Skip usage-only chunks (no content)
	if c.Usage != nil && (c.Message == nil || len(c.Message.Content) == 0) {
		return nil
	}

	chunk := s.newChunk(ChatCompletionChoice{Delta: &ChatCompletionMessage{}})

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
	if result.Status == provider.CompletionStatusFailed {
		return s.Error(shared.CompletionFailure(result))
	}

	finishReason := chatFinishReason(result, len(s.toolCallIndices) > 0)

	// Calls that streamed no argument bytes must still deliver parseable
	// arguments — clients rebuild them purely from deltas.
	if finishReason == FinishReasonToolCalls {
		for _, id := range s.pendingArgumentIDs() {
			idx := s.toolCallIndices[id]

			chunk := s.newChunk(ChatCompletionChoice{
				Delta: &ChatCompletionMessage{
					ToolCalls: []ToolCall{{Index: &idx, Function: &FunctionCall{Arguments: "{}"}}},
				},
			})

			if err := s.emitEvent(StreamEvent{
				Type:  StreamEventChunk,
				Chunk: chunk,
			}); err != nil {
				return err
			}
		}
	}

	finishChunk := s.newChunk(ChatCompletionChoice{
		Delta:        &ChatCompletionMessage{},
		FinishReason: &finishReason,
	})
	if err := s.emitEvent(StreamEvent{Type: StreamEventFinish, Chunk: finishChunk, Completion: result}); err != nil {
		return err
	}

	// Emit usage chunk if requested and available
	if includeUsage && result.Usage != nil {
		usageChunk := s.newChunk()
		usageChunk.Usage = toUsage(result.Usage)

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

func (s *StreamingAccumulator) newChunk(choices ...ChatCompletionChoice) *ChatCompletion {
	chunk := &ChatCompletion{
		Object:      "chat.completion.chunk",
		ID:          s.id,
		Model:       s.model,
		ServiceTier: shared.ServiceTier(s.tier),
		Choices:     choices,
	}
	if chunk.Choices == nil {
		chunk.Choices = []ChatCompletionChoice{}
	}
	return chunk
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
