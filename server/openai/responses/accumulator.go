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
	StreamEventResponseIncomplete StreamEventType = "response.incomplete"
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
	ToolCallID     string
	ToolCallCallID string
	ToolCallName   string
	Arguments      string
	OutputIndex    int

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
	started            bool
	hasOutputItem      bool // True if we emitted output_item.added for message
	hasContentPart     bool // True if we emitted content_part.added
	messageClosed      bool // True if we emitted output_item.done for message
	messageOutputIndex int  // Output index for the message item
	streamedText       strings.Builder

	// Track tool calls - map from effective call ID to output index
	toolCallIndices map[string]int
	toolCallStarted map[string]bool
	lastToolCallID  string // Track the last effective call ID for chunks without an explicit ID
	nextOutputIndex int    // Next available output index

	// Track reasoning state
	reasoningID              string
	reasoningSignature       string
	hasReasoningItem         bool
	hasReasoningTextPart     bool
	hasReasoningSummaryPart  bool
	reasoningOutputIndex     int
	reasoningClosed          bool
	streamedReasoningText    strings.Builder
	streamedReasoningSummary strings.Builder
}

// NewStreamingAccumulator creates a new StreamingAccumulator with an event handler
func NewStreamingAccumulator(handler StreamEventHandler) *StreamingAccumulator {
	return &StreamingAccumulator{
		handler:         handler,
		toolCallIndices: make(map[string]int),
		toolCallStarted: make(map[string]bool),
	}
}

func (s *StreamingAccumulator) reserveOutputIndex() int {
	outputIndex := s.nextOutputIndex
	s.nextOutputIndex++
	return outputIndex
}

func (s *StreamingAccumulator) start() error {
	if s.started {
		return nil
	}

	s.started = true

	if err := s.emitEvent(StreamEvent{Type: StreamEventResponseCreated}); err != nil {
		return err
	}

	return s.emitEvent(StreamEvent{Type: StreamEventResponseInProgress})
}

func (s *StreamingAccumulator) ensureMessageItem() error {
	if s.hasOutputItem {
		return nil
	}

	if err := s.closeReasoning(); err != nil {
		return err
	}

	s.hasOutputItem = true
	s.messageOutputIndex = s.reserveOutputIndex()

	return s.emitEvent(StreamEvent{
		Type:        StreamEventOutputItemAdded,
		OutputIndex: s.messageOutputIndex,
	})
}

func (s *StreamingAccumulator) ensureMessageContentPart() error {
	if s.hasContentPart {
		return nil
	}

	s.hasContentPart = true

	return s.emitEvent(StreamEvent{
		Type:        StreamEventContentPartAdded,
		OutputIndex: s.messageOutputIndex,
	})
}

func (s *StreamingAccumulator) trackToolCall(toolCall provider.ToolCall) (string, int, bool) {
	toolCallID := toolCall.CallID
	if toolCallID == "" {
		toolCallID = toolCall.ID
	}

	if toolCallID != "" {
		if _, exists := s.toolCallIndices[toolCallID]; !exists {
			s.toolCallIndices[toolCallID] = s.reserveOutputIndex()
		}
		s.lastToolCallID = toolCallID
	}

	currentToolCallID := toolCallID
	if currentToolCallID == "" {
		currentToolCallID = s.lastToolCallID
	} else {
		s.lastToolCallID = currentToolCallID
	}

	if currentToolCallID == "" {
		return "", 0, false
	}

	return currentToolCallID, s.toolCallIndices[currentToolCallID], true
}

func (s *StreamingAccumulator) ensureToolCallStarted(toolCallID string, toolCall provider.ToolCall, outputIndex int) error {
	if s.toolCallStarted[toolCallID] {
		return nil
	}

	s.toolCallStarted[toolCallID] = true

	itemID := toolCall.ID
	if itemID == "" {
		itemID = toolCallID
	}

	return s.emitEvent(StreamEvent{
		Type:           StreamEventFunctionCallAdded,
		ToolCallID:     itemID,
		ToolCallCallID: toolCallID,
		ToolCallName:   toolCall.Name,
		OutputIndex:    outputIndex,
	})
}

func (s *StreamingAccumulator) ensureReasoningItem() error {
	if s.hasReasoningItem {
		return nil
	}

	s.hasReasoningItem = true
	s.reasoningOutputIndex = s.reserveOutputIndex()

	if s.reasoningID == "" {
		s.reasoningID = "rs_" + uuid.NewString()
	}

	return s.emitEvent(StreamEvent{
		Type:        StreamEventReasoningItemAdded,
		ReasoningID: s.reasoningID,
		OutputIndex: s.reasoningOutputIndex,
	})
}

func (s *StreamingAccumulator) ensureReasoningTextPart() error {
	if s.hasReasoningTextPart {
		return nil
	}

	s.hasReasoningTextPart = true

	return s.emitEvent(StreamEvent{
		Type:         StreamEventReasoningContentPartAdded,
		ReasoningID:  s.reasoningID,
		OutputIndex:  s.reasoningOutputIndex,
		ContentIndex: 0,
	})
}

func (s *StreamingAccumulator) ensureReasoningSummaryPart() error {
	if s.hasReasoningSummaryPart {
		return nil
	}

	s.hasReasoningSummaryPart = true

	return s.emitEvent(StreamEvent{
		Type:         StreamEventReasoningSummaryPartAdded,
		ReasoningID:  s.reasoningID,
		OutputIndex:  s.reasoningOutputIndex,
		SummaryIndex: 0,
	})
}

// closeReasoning emits all the "done" events for reasoning if reasoning was in progress
// This should be called before starting the message output
func (s *StreamingAccumulator) closeReasoning() error {
	if !s.hasReasoningItem || s.reasoningClosed {
		return nil
	}
	s.reasoningClosed = true

	reasoningText := s.streamedReasoningText.String()
	reasoningSummary := s.streamedReasoningSummary.String()

	// Emit reasoning text done if we had text
	if s.streamedReasoningText.Len() > 0 {
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
	if s.streamedReasoningSummary.Len() > 0 {
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
	}); err != nil {
		return err
	}

	return nil
}

// closeMessage emits all the "done" events for the message if it was in progress.
// This should be called before starting function call output items so that the
// client sees output_item.done for the message BEFORE output_item.added for tool calls.
func (s *StreamingAccumulator) closeMessage() error {
	if !s.hasOutputItem || s.messageClosed {
		return nil
	}

	if s.streamedText.Len() == 0 {
		return nil
	}

	s.messageClosed = true
	text := s.streamedText.String()

	// text.done
	if err := s.emitEvent(StreamEvent{
		Type:        StreamEventTextDone,
		Text:        text,
		OutputIndex: s.messageOutputIndex,
	}); err != nil {
		return err
	}

	// content_part.done
	if err := s.emitEvent(StreamEvent{
		Type:        StreamEventContentPartDone,
		Text:        text,
		OutputIndex: s.messageOutputIndex,
	}); err != nil {
		return err
	}

	// output_item.done for message
	if err := s.emitEvent(StreamEvent{
		Type:        StreamEventOutputItemDone,
		Text:        text,
		OutputIndex: s.messageOutputIndex,
	}); err != nil {
		return err
	}

	return nil
}

// closePendingItems closes any in-progress reasoning and message items.
// Must be called before emitting function call events so that the client
// sees each output item completed before the next one starts.
func (s *StreamingAccumulator) closePendingItems() error {
	if err := s.closeReasoning(); err != nil {
		return err
	}

	return s.closeMessage()
}

// Add processes a completion chunk and emits appropriate events
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	if err := s.start(); err != nil {
		return err
	}

	// Check for message content
	if c.Message != nil {
		// Process text content
		for _, content := range c.Message.Content {
			if content.Text != "" {
				s.streamedText.WriteString(content.Text)

				if err := s.ensureMessageItem(); err != nil {
					return err
				}

				if err := s.ensureMessageContentPart(); err != nil {
					return err
				}

				// Emit text delta
				if err := s.emitEvent(StreamEvent{
					Type:        StreamEventTextDelta,
					Delta:       content.Text,
					OutputIndex: s.messageOutputIndex,
				}); err != nil {
					return err
				}
			}

			// Process tool calls
			if content.ToolCall != nil {
				toolCall := content.ToolCall

				// Close any pending reasoning/message items before starting tool call events.
				// The Responses API requires each output item to be completed before the next starts.
				if err := s.closePendingItems(); err != nil {
					return err
				}

				currentToolCallID, outputIndex, ok := s.trackToolCall(*toolCall)
				if ok {
					if err := s.ensureToolCallStarted(currentToolCallID, *toolCall, outputIndex); err != nil {
						return err
					}

					if toolCall.Arguments != "" {
						itemID := toolCall.ID
						if itemID == "" {
							itemID = currentToolCallID
						}

						if err := s.emitEvent(StreamEvent{
							Type:           StreamEventFunctionCallArgumentsDelta,
							ToolCallID:     itemID,
							ToolCallCallID: currentToolCallID,
							Delta:          toolCall.Arguments,
							OutputIndex:    outputIndex,
						}); err != nil {
							return err
						}
					}
				}
			}

			// Process reasoning content
			if content.Reasoning != nil {
				reasoning := content.Reasoning

				// Update reasoning ID if we receive one (may arrive in later chunks)
				if reasoning.ID != "" && s.reasoningID == "" {
					s.reasoningID = reasoning.ID
				}

				// Capture signature/encrypted_content for conversation continuity
				if reasoning.Signature != "" {
					s.reasoningSignature = reasoning.Signature

					if err := s.ensureReasoningItem(); err != nil {
						return err
					}
				}

				if reasoning.Text != "" {
					if err := s.ensureReasoningItem(); err != nil {
						return err
					}

					if err := s.ensureReasoningTextPart(); err != nil {
						return err
					}

					s.streamedReasoningText.WriteString(reasoning.Text)

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

				if reasoning.Summary != "" {
					if err := s.ensureReasoningItem(); err != nil {
						return err
					}

					if err := s.ensureReasoningSummaryPart(); err != nil {
						return err
					}

					s.streamedReasoningSummary.WriteString(reasoning.Summary)

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
	// and they weren't already emitted by closeMessage() (which fires when
	// tool calls arrive after text).
	if s.streamedText.Len() > 0 && !s.messageClosed {
		s.messageClosed = true

		// text.done
		if err := s.emitEvent(StreamEvent{
			Type:        StreamEventTextDone,
			Text:        text,
			OutputIndex: s.messageOutputIndex,
			Completion:  result,
		}); err != nil {
			return err
		}

		// content_part.done
		if err := s.emitEvent(StreamEvent{
			Type:        StreamEventContentPartDone,
			Text:        text,
			OutputIndex: s.messageOutputIndex,
			Completion:  result,
		}); err != nil {
			return err
		}

		// output_item.done for message
		if err := s.emitEvent(StreamEvent{
			Type:        StreamEventOutputItemDone,
			Text:        text,
			OutputIndex: s.messageOutputIndex,
			Completion:  result,
		}); err != nil {
			return err
		}
	}

	// Emit reasoning done events if reasoning wasn't already closed
	if err := s.closeReasoning(); err != nil {
		return err
	}

	// Emit function_call_arguments.done and function_call.done for each tool call
	if result.Message != nil {
		for _, call := range result.Message.ToolCalls() {
			callID := call.CallID
			if callID == "" {
				callID = call.ID
			}
			itemID := call.ID
			if itemID == "" {
				itemID = callID
			}

			outputIndex := s.toolCallIndices[callID]

			// function_call_arguments.done
			if err := s.emitEvent(StreamEvent{
				Type:           StreamEventFunctionCallArgumentsDone,
				ToolCallID:     itemID,
				ToolCallCallID: callID,
				ToolCallName:   call.Name,
				Arguments:      call.Arguments,
				OutputIndex:    outputIndex,
			}); err != nil {
				return err
			}

			// function_call.done (output_item.done for the function call)
			if err := s.emitEvent(StreamEvent{
				Type:           StreamEventFunctionCallDone,
				ToolCallID:     itemID,
				ToolCallCallID: callID,
				ToolCallName:   call.Name,
				Arguments:      call.Arguments,
				OutputIndex:    outputIndex,
				Completion:     result,
			}); err != nil {
				return err
			}
		}
	}

	terminalType := StreamEventResponseCompleted
	if result.Status == provider.CompletionStatusIncomplete {
		terminalType = StreamEventResponseIncomplete
	}

	if err := s.emitEvent(StreamEvent{
		Type:               terminalType,
		Text:               text,
		ReasoningID:        s.reasoningID,
		ReasoningSignature: s.reasoningSignature,
		Completion:         result,
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
