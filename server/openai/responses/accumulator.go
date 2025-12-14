package responses

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

// StreamEventType represents the type of streaming event
type StreamEventType string

const (
	StreamEventResponseCreated    StreamEventType = "response.created"
	StreamEventResponseInProgress StreamEventType = "response.in_progress"
	StreamEventResponseCompleted  StreamEventType = "response.completed"
	StreamEventOutputItemAdded    StreamEventType = "output_item.added"
	StreamEventOutputItemDone     StreamEventType = "output_item.done"
	StreamEventContentPartAdded   StreamEventType = "content_part.added"
	StreamEventContentPartDone    StreamEventType = "content_part.done"
	StreamEventTextDelta          StreamEventType = "text.delta"
	StreamEventTextDone           StreamEventType = "text.done"

	// Function call events
	StreamEventFunctionCallAdded          StreamEventType = "function_call.added"
	StreamEventFunctionCallArgumentsDelta StreamEventType = "function_call_arguments.delta"
	StreamEventFunctionCallArgumentsDone  StreamEventType = "function_call_arguments.done"
	StreamEventFunctionCallDone           StreamEventType = "function_call.done"
)

// StreamEvent represents a streaming event with its data
type StreamEvent struct {
	Type StreamEventType

	// For text delta events
	Delta string

	// For completion/done events - the full accumulated text
	Text string

	// For function call events
	ToolCallID   string
	ToolCallName string
	Arguments    string
	OutputIndex  int

	// The accumulated completion state
	Completion *provider.Completion
}

// StreamEventHandler is called for each streaming event
type StreamEventHandler func(event StreamEvent) error

// StreamingAccumulator wraps provider.CompletionAccumulator and emits events
type StreamingAccumulator struct {
	accumulator provider.CompletionAccumulator

	handler StreamEventHandler

	// Track state for event emission
	started        bool
	hasOutputItem  bool // True if we emitted output_item.added for message
	hasContentPart bool // True if we have actual text content
	hasTextContent bool // True if we received any text delta

	// Track tool calls - map from tool call ID to output index
	toolCallIndices map[string]int
	toolCallStarted map[string]bool
	lastToolCallID  string // Track the last tool call ID for chunks without ID
	nextOutputIndex int    // Next available output index
}

// NewStreamingAccumulator creates a new StreamingAccumulator with an event handler
func NewStreamingAccumulator(handler StreamEventHandler) *StreamingAccumulator {
	return &StreamingAccumulator{
		handler:         handler,
		toolCallIndices: make(map[string]int),
		toolCallStarted: make(map[string]bool),
		nextOutputIndex: 0,
	}
}

// Add processes a completion chunk and emits appropriate events
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	// Emit initial events on first add
	if !s.started {
		s.started = true

		// response.created
		if err := s.emitEvent(StreamEvent{
			Type: StreamEventResponseCreated,
		}); err != nil {
			return err
		}

		// response.in_progress
		if err := s.emitEvent(StreamEvent{
			Type: StreamEventResponseInProgress,
		}); err != nil {
			return err
		}
	}

	// Check for message content
	if c.Message != nil {
		// Process text content
		for _, content := range c.Message.Content {
			if content.Text != "" {
				// Emit output_item.added on first text (message container)
				if !s.hasOutputItem {
					s.hasOutputItem = true
					s.nextOutputIndex = 1 // Message takes index 0, next tool call starts at 1

					if err := s.emitEvent(StreamEvent{
						Type: StreamEventOutputItemAdded,
					}); err != nil {
						return err
					}
				}

				// Emit content_part.added on first text
				if !s.hasContentPart {
					s.hasContentPart = true

					if err := s.emitEvent(StreamEvent{
						Type: StreamEventContentPartAdded,
					}); err != nil {
						return err
					}
				}

				s.hasTextContent = true

				// Emit text delta
				if err := s.emitEvent(StreamEvent{
					Type:  StreamEventTextDelta,
					Delta: content.Text,
				}); err != nil {
					return err
				}
			}

			// Process tool calls
			if content.ToolCall != nil {
				toolCall := content.ToolCall

				// If we have an ID, this is a new tool call
				if toolCall.ID != "" {
					if _, exists := s.toolCallIndices[toolCall.ID]; !exists {
						// Assign output index
						outputIndex := s.nextOutputIndex
						s.toolCallIndices[toolCall.ID] = outputIndex
						s.nextOutputIndex++
						s.lastToolCallID = toolCall.ID
					}
				}

				// Find the current tool call ID (either from this chunk or the last one)
				currentToolCallID := toolCall.ID
				if currentToolCallID == "" {
					currentToolCallID = s.lastToolCallID
				} else {
					s.lastToolCallID = toolCall.ID
				}

				if currentToolCallID != "" {
					outputIndex := s.toolCallIndices[currentToolCallID]

					// Emit function_call.added on first occurrence
					if !s.toolCallStarted[currentToolCallID] {
						s.toolCallStarted[currentToolCallID] = true

						if err := s.emitEvent(StreamEvent{
							Type:         StreamEventFunctionCallAdded,
							ToolCallID:   currentToolCallID,
							ToolCallName: toolCall.Name,
							OutputIndex:  outputIndex,
						}); err != nil {
							return err
						}
					}

					// Emit arguments delta if we have arguments
					if toolCall.Arguments != "" {
						if err := s.emitEvent(StreamEvent{
							Type:        StreamEventFunctionCallArgumentsDelta,
							ToolCallID:  currentToolCallID,
							Delta:       toolCall.Arguments,
							OutputIndex: outputIndex,
						}); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	// Add to underlying accumulator
	s.accumulator.Add(c)

	return nil
}

// Complete signals that streaming is done and emits final events
func (s *StreamingAccumulator) Complete() error {
	result := s.accumulator.Result()
	fullText := ""

	if result.Message != nil {
		fullText = result.Message.Text()
	}

	// Only emit text/content done events if we actually had text content
	if s.hasTextContent {
		// text.done
		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventTextDone,
			Text:       fullText,
			Completion: result,
		}); err != nil {
			return err
		}

		// content_part.done
		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventContentPartDone,
			Text:       fullText,
			Completion: result,
		}); err != nil {
			return err
		}

		// output_item.done for message
		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventOutputItemDone,
			Completion: result,
		}); err != nil {
			return err
		}
	}

	// Emit function_call_arguments.done and function_call.done for each tool call
	if result.Message != nil {
		for _, call := range result.Message.ToolCalls() {
			outputIndex := s.toolCallIndices[call.ID]

			// function_call_arguments.done
			if err := s.emitEvent(StreamEvent{
				Type:         StreamEventFunctionCallArgumentsDone,
				ToolCallID:   call.ID,
				ToolCallName: call.Name,
				Arguments:    call.Arguments,
				OutputIndex:  outputIndex,
			}); err != nil {
				return err
			}

			// function_call.done (output_item.done for the function call)
			if err := s.emitEvent(StreamEvent{
				Type:         StreamEventFunctionCallDone,
				ToolCallID:   call.ID,
				ToolCallName: call.Name,
				Arguments:    call.Arguments,
				OutputIndex:  outputIndex,
				Completion:   result,
			}); err != nil {
				return err
			}
		}
	}

	// response.completed
	if err := s.emitEvent(StreamEvent{
		Type:       StreamEventResponseCompleted,
		Completion: result,
	}); err != nil {
		return err
	}

	return nil
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
