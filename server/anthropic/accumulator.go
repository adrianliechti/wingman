package anthropic

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

// StreamEventType represents the type of streaming event
type StreamEventType string

const (
	StreamEventMessageStart      StreamEventType = "message_start"
	StreamEventContentBlockStart StreamEventType = "content_block_start"
	StreamEventContentBlockDelta StreamEventType = "content_block_delta"
	StreamEventContentBlockStop  StreamEventType = "content_block_stop"
	StreamEventMessageDelta      StreamEventType = "message_delta"
	StreamEventMessageStop       StreamEventType = "message_stop"
	StreamEventError             StreamEventType = "error"
)

// StreamEvent represents a streaming event with its data
type StreamEvent struct {
	Type StreamEventType

	// For message_start
	Message *Message

	// For content_block_start/stop
	Index        int
	ContentBlock *ContentBlock

	// For content_block_delta
	Delta *Delta

	// For message_delta
	MessageDelta *MessageDelta
	DeltaUsage   *DeltaUsage

	// For error events
	Error *Error

	// The accumulated completion (available on final events)
	Completion *provider.Completion
}

// StreamEventHandler is called for each streaming event
type StreamEventHandler func(event StreamEvent) error

// StreamingAccumulator manages streaming state and emits events
type StreamingAccumulator struct {
	accumulator provider.CompletionAccumulator

	handler StreamEventHandler

	// Configuration
	messageID string
	model     string

	// State tracking
	started           bool
	currentBlockIndex int
	currentBlockType  string // "text" or "tool_use"
	hasContent        bool

	// Tool call tracking
	toolCallID   string
	toolCallName string
	toolCallArgs string

	// Usage tracking
	outputTokens int
	stopReason   StopReason
}

// NewStreamingAccumulator creates a new StreamingAccumulator with an event handler
func NewStreamingAccumulator(messageID, model string, handler StreamEventHandler) *StreamingAccumulator {
	return &StreamingAccumulator{
		handler:   handler,
		messageID: messageID,
		model:     model,

		currentBlockIndex: -1,
		stopReason:        StopReasonEndTurn,
	}
}

// Add processes a completion chunk and emits appropriate events
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	// Emit message_start on first add
	if !s.started {
		s.started = true

		if err := s.emitEvent(StreamEvent{
			Type: StreamEventMessageStart,
			Message: &Message{
				ID:   s.messageID,
				Type: "message",
				Role: "assistant",

				Model:   s.model,
				Content: []ContentBlock{},

				Usage: Usage{
					InputTokens:  0,
					OutputTokens: 0,
				},
			},
		}); err != nil {
			return err
		}
	}

	// Update model if provided
	if c.Model != "" {
		s.model = c.Model
	}

	// Update output tokens if provided
	if c.Usage != nil && c.Usage.OutputTokens > 0 {
		s.outputTokens = c.Usage.OutputTokens
	}

	// Skip empty content chunks
	if c.Message == nil || len(c.Message.Content) == 0 {
		s.accumulator.Add(c)
		return nil
	}

	// Process content
	for _, content := range c.Message.Content {
		// Handle text content
		if content.Text != "" {
			// Start text block if needed
			if s.currentBlockType != "text" {
				// Close previous block if any
				if s.currentBlockIndex >= 0 {
					if err := s.emitEvent(StreamEvent{
						Type:  StreamEventContentBlockStop,
						Index: s.currentBlockIndex,
					}); err != nil {
						return err
					}
				}

				s.currentBlockIndex++
				s.currentBlockType = "text"
				s.hasContent = true

				if err := s.emitEvent(StreamEvent{
					Type:  StreamEventContentBlockStart,
					Index: s.currentBlockIndex,
					ContentBlock: &ContentBlock{
						Type: "text",
						Text: "",
					},
				}); err != nil {
					return err
				}
			}

			// Send text delta
			if err := s.emitEvent(StreamEvent{
				Type:  StreamEventContentBlockDelta,
				Index: s.currentBlockIndex,
				Delta: &Delta{
					Type: "text_delta",
					Text: content.Text,
				},
			}); err != nil {
				return err
			}
		}

		// Handle tool calls
		if content.ToolCall != nil {
			s.stopReason = StopReasonToolUse

			// Check if this is a new tool call or continuation
			isNewToolCall := content.ToolCall.ID != "" && content.ToolCall.ID != s.toolCallID

			if isNewToolCall {
				// Close previous block if any
				if s.currentBlockIndex >= 0 {
					if err := s.emitEvent(StreamEvent{
						Type:  StreamEventContentBlockStop,
						Index: s.currentBlockIndex,
					}); err != nil {
						return err
					}
				}

				s.currentBlockIndex++
				s.currentBlockType = "tool_use"

				s.toolCallID = content.ToolCall.ID
				if s.toolCallID == "" {
					s.toolCallID = generateToolUseID()
				}

				s.toolCallName = content.ToolCall.Name
				s.toolCallArgs = ""
				s.hasContent = true

				// Send content_block_start for tool_use
				if err := s.emitEvent(StreamEvent{
					Type:  StreamEventContentBlockStart,
					Index: s.currentBlockIndex,
					ContentBlock: &ContentBlock{
						Type:  "tool_use",
						ID:    s.toolCallID,
						Name:  s.toolCallName,
						Input: map[string]any{},
					},
				}); err != nil {
					return err
				}
			}

			// Send input_json_delta if there are arguments
			if content.ToolCall.Arguments != "" {
				s.toolCallArgs += content.ToolCall.Arguments

				if err := s.emitEvent(StreamEvent{
					Type:  StreamEventContentBlockDelta,
					Index: s.currentBlockIndex,
					Delta: &Delta{
						Type:        "input_json_delta",
						PartialJSON: content.ToolCall.Arguments,
					},
				}); err != nil {
					return err
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

	// Close last content block if any
	if s.currentBlockIndex >= 0 {
		if err := s.emitEvent(StreamEvent{
			Type:  StreamEventContentBlockStop,
			Index: s.currentBlockIndex,
		}); err != nil {
			return err
		}
	}

	// If no content was generated, send an empty text block
	if !s.hasContent {
		if err := s.emitEvent(StreamEvent{
			Type:  StreamEventContentBlockStart,
			Index: 0,
			ContentBlock: &ContentBlock{
				Type: "text",
				Text: "",
			},
		}); err != nil {
			return err
		}

		if err := s.emitEvent(StreamEvent{
			Type:  StreamEventContentBlockStop,
			Index: 0,
		}); err != nil {
			return err
		}
	}

	// Determine final stop reason from accumulated result
	if result.Message != nil {
		s.stopReason = toStopReason(result.Message.Content)
	}

	// Send message_delta with stop_reason and usage
	if err := s.emitEvent(StreamEvent{
		Type: StreamEventMessageDelta,
		MessageDelta: &MessageDelta{
			StopReason: s.stopReason,
		},
		DeltaUsage: &DeltaUsage{
			OutputTokens: s.outputTokens,
		},
		Completion: result,
	}); err != nil {
		return err
	}

	// Send message_stop
	if err := s.emitEvent(StreamEvent{
		Type:       StreamEventMessageStop,
		Completion: result,
	}); err != nil {
		return err
	}

	return nil
}

// Error emits an error event
func (s *StreamingAccumulator) Error(err error) error {
	return s.emitEvent(StreamEvent{
		Type: StreamEventError,
		Error: &Error{
			Type:    "api_error",
			Message: err.Error(),
		},
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
