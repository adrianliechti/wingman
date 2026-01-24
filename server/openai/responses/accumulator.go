package responses

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"
)

// StreamEventType represents the type of streaming event
type StreamEventType string

const (
	StreamEventResponseCreated    StreamEventType = "response.created"
	StreamEventResponseInProgress StreamEventType = "response.in_progress"
	StreamEventResponseCompleted  StreamEventType = "response.completed"
	StreamEventResponseFailed     StreamEventType = "response.failed"
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

	// Reasoning events
	StreamEventReasoningItemAdded        StreamEventType = "reasoning_item.added"
	StreamEventReasoningItemDone         StreamEventType = "reasoning_item.done"
	StreamEventReasoningSummaryPartAdded StreamEventType = "reasoning_summary_part.added"
	StreamEventReasoningSummaryPartDone  StreamEventType = "reasoning_summary_part.done"
	StreamEventReasoningSummaryDelta     StreamEventType = "reasoning_summary_text.delta"
	StreamEventReasoningSummaryDone      StreamEventType = "reasoning_summary_text.done"
	StreamEventReasoningTextDelta        StreamEventType = "reasoning_text.delta"
	StreamEventReasoningTextDone         StreamEventType = "reasoning_text.done"
	StreamEventReasoningContentPartAdded StreamEventType = "reasoning_content_part.added"
	StreamEventReasoningContentPartDone  StreamEventType = "reasoning_content_part.done"
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

	// For reasoning events
	ReasoningID        string
	ReasoningText      string
	ReasoningSummary   string
	ReasoningSignature string
	SummaryIndex       int
	ContentIndex       int

	// For error events
	Error error

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
	hasContentPart bool // True if we emitted content_part.added
	streamedText   strings.Builder

	// Track tool calls - map from tool call ID to output index
	toolCallIndices map[string]int
	toolCallStarted map[string]bool
	lastToolCallID  string // Track the last tool call ID for chunks without ID
	nextOutputIndex int    // Next available output index

	// Track reasoning state
	reasoningID             string
	reasoningSignature      string
	hasReasoningItem        bool
	hasReasoningContentPart bool
	hasReasoningText        bool
	hasReasoningSummaryPart bool
	hasReasoningSummary     bool
	reasoningOutputIndex    int
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
				s.streamedText.WriteString(content.Text)

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

			// Process reasoning content
			if content.Reasoning != nil {
				reasoning := content.Reasoning

				// Capture signature/encrypted_content for conversation continuity
				if reasoning.Signature != "" {
					s.reasoningSignature = reasoning.Signature
				}

				// Handle reasoning text
				if reasoning.Text != "" {
					// Emit reasoning item added on first reasoning content
					if !s.hasReasoningItem {
						s.hasReasoningItem = true
						s.reasoningOutputIndex = s.nextOutputIndex
						s.nextOutputIndex++
						s.reasoningID = "rs_" + uuid.NewString()

						if err := s.emitEvent(StreamEvent{
							Type:        StreamEventReasoningItemAdded,
							ReasoningID: s.reasoningID,
							OutputIndex: s.reasoningOutputIndex,
						}); err != nil {
							return err
						}
					}

					// Emit content_part.added on first reasoning text
					if !s.hasReasoningContentPart {
						s.hasReasoningContentPart = true

						if err := s.emitEvent(StreamEvent{
							Type:         StreamEventReasoningContentPartAdded,
							ReasoningID:  s.reasoningID,
							OutputIndex:  s.reasoningOutputIndex,
							ContentIndex: 0,
						}); err != nil {
							return err
						}
					}

					s.hasReasoningText = true

					// Emit reasoning text delta
					if err := s.emitEvent(StreamEvent{
						Type:         StreamEventReasoningTextDelta,
						ReasoningID:  s.reasoningID,
						Delta:        reasoning.Text,
						OutputIndex:  s.reasoningOutputIndex,
						ContentIndex: 0,
					}); err != nil {
						return err
					}
				}

				// Handle reasoning summary
				if reasoning.Summary != "" {
					// Emit reasoning item added if not already done
					if !s.hasReasoningItem {
						s.hasReasoningItem = true
						s.reasoningOutputIndex = s.nextOutputIndex
						s.nextOutputIndex++
						s.reasoningID = "rs_" + uuid.NewString()

						if err := s.emitEvent(StreamEvent{
							Type:        StreamEventReasoningItemAdded,
							ReasoningID: s.reasoningID,
							OutputIndex: s.reasoningOutputIndex,
						}); err != nil {
							return err
						}
					}

					// Emit summary_part.added on first summary
					if !s.hasReasoningSummaryPart {
						s.hasReasoningSummaryPart = true

						if err := s.emitEvent(StreamEvent{
							Type:         StreamEventReasoningSummaryPartAdded,
							ReasoningID:  s.reasoningID,
							OutputIndex:  s.reasoningOutputIndex,
							SummaryIndex: 0,
						}); err != nil {
							return err
						}
					}

					s.hasReasoningSummary = true

					// Emit reasoning summary delta
					if err := s.emitEvent(StreamEvent{
						Type:         StreamEventReasoningSummaryDelta,
						ReasoningID:  s.reasoningID,
						Delta:        reasoning.Summary,
						OutputIndex:  s.reasoningOutputIndex,
						SummaryIndex: 0,
					}); err != nil {
						return err
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
	text := s.streamedText.String()

	// Only emit text/content done events if we actually had text content
	if s.streamedText.Len() > 0 {
		// text.done
		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventTextDone,
			Text:       text,
			Completion: result,
		}); err != nil {
			return err
		}

		// content_part.done
		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventContentPartDone,
			Text:       text,
			Completion: result,
		}); err != nil {
			return err
		}

		// output_item.done for message
		if err := s.emitEvent(StreamEvent{
			Type:       StreamEventOutputItemDone,
			Text:       text,
			Completion: result,
		}); err != nil {
			return err
		}
	}

	// Emit reasoning done events if we had reasoning content
	if s.hasReasoningItem {
		// Get reasoning from accumulated result
		var reasoningText, reasoningSummary string
		if result.Message != nil {
			for _, content := range result.Message.Content {
				if content.Reasoning != nil {
					reasoningText = content.Reasoning.Text
					reasoningSummary = content.Reasoning.Summary
					break
				}
			}
		}

		// Emit reasoning text done if we had text
		if s.hasReasoningText {
			if err := s.emitEvent(StreamEvent{
				Type:          StreamEventReasoningTextDone,
				ReasoningID:   s.reasoningID,
				ReasoningText: reasoningText,
				OutputIndex:   s.reasoningOutputIndex,
				ContentIndex:  0,
			}); err != nil {
				return err
			}

			// content_part.done for reasoning
			if err := s.emitEvent(StreamEvent{
				Type:          StreamEventReasoningContentPartDone,
				ReasoningID:   s.reasoningID,
				ReasoningText: reasoningText,
				OutputIndex:   s.reasoningOutputIndex,
				ContentIndex:  0,
			}); err != nil {
				return err
			}
		}

		// Emit summary done if we had summary
		if s.hasReasoningSummary {
			if err := s.emitEvent(StreamEvent{
				Type:             StreamEventReasoningSummaryDone,
				ReasoningID:      s.reasoningID,
				ReasoningSummary: reasoningSummary,
				OutputIndex:      s.reasoningOutputIndex,
				SummaryIndex:     0,
			}); err != nil {
				return err
			}

			// summary_part.done
			if err := s.emitEvent(StreamEvent{
				Type:             StreamEventReasoningSummaryPartDone,
				ReasoningID:      s.reasoningID,
				ReasoningSummary: reasoningSummary,
				OutputIndex:      s.reasoningOutputIndex,
				SummaryIndex:     0,
			}); err != nil {
				return err
			}
		}

		// output_item.done for reasoning
		if err := s.emitEvent(StreamEvent{
			Type:               StreamEventReasoningItemDone,
			ReasoningID:        s.reasoningID,
			ReasoningText:      reasoningText,
			ReasoningSummary:   reasoningSummary,
			ReasoningSignature: s.reasoningSignature,
			OutputIndex:        s.reasoningOutputIndex,
			Completion:         result,
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
		Text:       text,
		Completion: result,
	}); err != nil {
		return err
	}

	return nil
}

// Error emits an error event
func (s *StreamingAccumulator) Error(err error) error {
	return s.emitEvent(StreamEvent{
		Type:  StreamEventResponseFailed,
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
