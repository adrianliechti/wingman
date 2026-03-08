package responses

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
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
	started            bool
	hasOutputItem      bool // True if we emitted output_item.added for message
	hasContentPart     bool // True if we emitted content_part.added
	messageOutputIndex int  // Output index for the message item
	streamedText       strings.Builder

	// Track tool calls - map from tool call ID to output index
	toolCallIndices map[string]int
	toolCallStarted map[string]bool
	lastToolCallID  string // Track the last tool call ID for chunks without ID
	nextOutputIndex int    // Next available output index

	// Track reasoning state
	reasoningID              string
	reasoningSignature       string
	hasReasoningItem         bool
	hasReasoningTextPart     bool
	hasReasoningText         bool
	hasReasoningSummaryPart  bool
	hasReasoningSummary      bool
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
		nextOutputIndex: 0,
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
	if toolCall.ID != "" {
		if _, exists := s.toolCallIndices[toolCall.ID]; !exists {
			s.toolCallIndices[toolCall.ID] = s.reserveOutputIndex()
		}
		s.lastToolCallID = toolCall.ID
	}

	currentToolCallID := toolCall.ID
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

func (s *StreamingAccumulator) ensureToolCallStarted(toolCallID, toolCallName string, outputIndex int) error {
	if s.toolCallStarted[toolCallID] {
		return nil
	}

	s.toolCallStarted[toolCallID] = true

	return s.emitEvent(StreamEvent{
		Type:         StreamEventFunctionCallAdded,
		ToolCallID:   toolCallID,
		ToolCallName: toolCallName,
		OutputIndex:  outputIndex,
	})
}

func (s *StreamingAccumulator) ensureReasoningItem() error {
	if s.hasReasoningItem {
		return nil
	}

	s.hasReasoningItem = true
	s.reasoningOutputIndex = s.reserveOutputIndex()

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
	}); err != nil {
		return err
	}

	return nil
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

				currentToolCallID, outputIndex, ok := s.trackToolCall(*toolCall)
				if ok {
					if err := s.ensureToolCallStarted(currentToolCallID, toolCall.Name, outputIndex); err != nil {
						return err
					}

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

					s.hasReasoningText = true
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

					s.hasReasoningSummary = true
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
	if s.streamedText.Len() > 0 {
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
