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

	ThinkingEnabled bool

	// State tracking
	started    bool
	hasContent bool

	// Content blocks may interleave; deltas are routed to stable indexes
	// and open blocks are closed on Complete
	nextBlockIndex int
	openBlocks     []int

	textIndex int

	thinkingID     string
	thinkingIndex  int
	thinkingSigned bool

	compactionID    string
	compactionIndex int

	toolIndexByID  map[string]int
	lastToolCallID string

	// Usage tracking
	inputTokens  int
	outputTokens int
	stopReason   StopReason
}

// NewStreamingAccumulator creates a new StreamingAccumulator with an event handler
func NewStreamingAccumulator(messageID, model string, handler StreamEventHandler) *StreamingAccumulator {
	return &StreamingAccumulator{
		handler:   handler,
		messageID: messageID,
		model:     model,

		textIndex:       -1,
		thinkingIndex:   -1,
		compactionIndex: -1,

		toolIndexByID: make(map[string]int),

		stopReason: StopReasonEndTurn,
	}
}

func (s *StreamingAccumulator) startBlock(block *ContentBlock) (int, error) {
	for len(s.openBlocks) > 0 {
		if err := s.stopBlock(s.openBlocks[0]); err != nil {
			return 0, err
		}
	}

	index := s.nextBlockIndex
	s.nextBlockIndex++

	s.hasContent = true
	s.openBlocks = append(s.openBlocks, index)

	return index, s.emitEvent(StreamEvent{
		Type:         StreamEventContentBlockStart,
		Index:        index,
		ContentBlock: block,
	})
}

func (s *StreamingAccumulator) stopBlock(index int) error {
	found := false

	for i, open := range s.openBlocks {
		if open == index {
			s.openBlocks = append(s.openBlocks[:i], s.openBlocks[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		return nil
	}

	if s.textIndex == index {
		s.textIndex = -1
	}

	if s.thinkingIndex == index {
		s.thinkingIndex = -1
	}

	if s.compactionIndex == index {
		s.compactionIndex = -1
	}

	return s.emitEvent(StreamEvent{
		Type:  StreamEventContentBlockStop,
		Index: index,
	})
}

// Add processes a completion chunk and emits appropriate events
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	// Emit message_start on first add
	if !s.started {
		s.started = true

		// Get input tokens from first chunk if available
		inputTokens := 0
		var cacheReadInputTokens, cacheCreationInputTokens int
		if c.Usage != nil {
			inputTokens = anthropicInputTokens(c.Usage)
			s.inputTokens = inputTokens
			cacheReadInputTokens = c.Usage.CacheReadInputTokens
			cacheCreationInputTokens = c.Usage.CacheCreationInputTokens
		}

		if err := s.emitEvent(StreamEvent{
			Type: StreamEventMessageStart,
			Message: &Message{
				ID:   s.messageID,
				Type: "message",
				Role: "assistant",

				Model:   s.model,
				Content: []ContentBlock{},

				Usage: Usage{
					InputTokens:  inputTokens,
					OutputTokens: 0,

					CacheReadInputTokens:     cacheReadInputTokens,
					CacheCreationInputTokens: cacheCreationInputTokens,
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

	// Update usage tokens if provided
	if c.Usage != nil {
		if c.Usage.InputTokens > 0 {
			s.inputTokens = anthropicInputTokens(c.Usage)
		}
		if c.Usage.OutputTokens > 0 {
			s.outputTokens = c.Usage.OutputTokens
		}
	}

	// Skip empty content chunks
	if c.Message == nil || len(c.Message.Content) == 0 {
		s.accumulator.Add(c)
		return nil
	}

	// Process content
	for _, content := range c.Message.Content {
		if content.Compaction != nil && (content.Compaction.Content != "" || content.Compaction.Signature != "") {
			if s.compactionIndex >= 0 && content.Compaction.ID != "" && s.compactionID != "" && content.Compaction.ID != s.compactionID {
				if err := s.stopBlock(s.compactionIndex); err != nil {
					return err
				}

				s.compactionIndex = -1
			}

			if s.compactionIndex < 0 {
				index, err := s.startBlock(&ContentBlock{
					Type: "compaction",
				})

				if err != nil {
					return err
				}

				s.compactionIndex = index
				s.compactionID = content.Compaction.ID
			}

			if err := s.emitEvent(StreamEvent{
				Type:  StreamEventContentBlockDelta,
				Index: s.compactionIndex,
				Delta: &Delta{
					Type:             "compaction_delta",
					Content:          content.Compaction.Content,
					EncryptedContent: content.Compaction.Signature,
				},
			}); err != nil {
				return err
			}
		}

		if s.ThinkingEnabled && content.Reasoning != nil && content.Reasoning.Redacted && content.Reasoning.Signature != "" {
			index, err := s.startBlock(&ContentBlock{
				Type: "redacted_thinking",
				Data: content.Reasoning.Signature,
			})

			if err != nil {
				return err
			}

			if err := s.stopBlock(index); err != nil {
				return err
			}
		}

		if s.ThinkingEnabled && content.Reasoning != nil && !content.Reasoning.Redacted && (content.Reasoning.Text != "" || content.Reasoning.Signature != "") {
			reasoning := content.Reasoning

			// A signature ends a thinking block; a new ID starts the next item
			if s.thinkingIndex >= 0 && (s.thinkingSigned || (reasoning.ID != "" && s.thinkingID != "" && reasoning.ID != s.thinkingID)) {
				if err := s.stopBlock(s.thinkingIndex); err != nil {
					return err
				}

				s.thinkingIndex = -1
			}

			if s.thinkingIndex < 0 {
				index, err := s.startBlock(&ContentBlock{
					Type:      "thinking",
					Thinking:  "",
					Signature: "",
				})

				if err != nil {
					return err
				}

				s.thinkingIndex = index
				s.thinkingID = reasoning.ID
				s.thinkingSigned = false
			}

			if reasoning.ID != "" && s.thinkingID == "" {
				s.thinkingID = reasoning.ID
			}

			if reasoning.Text != "" {
				if err := s.emitEvent(StreamEvent{
					Type:  StreamEventContentBlockDelta,
					Index: s.thinkingIndex,
					Delta: &Delta{
						Type:     "thinking_delta",
						Thinking: reasoning.Text,
					},
				}); err != nil {
					return err
				}
			}

			if reasoning.Signature != "" {
				s.thinkingSigned = true

				id := reasoning.ID
				if id == "" {
					id = s.thinkingID
				}

				if err := s.emitEvent(StreamEvent{
					Type:  StreamEventContentBlockDelta,
					Index: s.thinkingIndex,
					Delta: &Delta{
						Type:      "signature_delta",
						Signature: encodeSignature(id, reasoning.Signature),
					},
				}); err != nil {
					return err
				}
			}
		}

		if content.Text != "" {
			if s.textIndex < 0 {
				index, err := s.startBlock(&ContentBlock{
					Type: "text",
					Text: ptr(""),
				})

				if err != nil {
					return err
				}

				s.textIndex = index
			}

			if err := s.emitEvent(StreamEvent{
				Type:  StreamEventContentBlockDelta,
				Index: s.textIndex,
				Delta: &Delta{
					Type: "text_delta",
					Text: content.Text,
				},
			}); err != nil {
				return err
			}
		}

		if content.ToolCall != nil {
			s.stopReason = StopReasonToolUse

			id := content.ToolCall.ID

			if id == "" {
				id = s.lastToolCallID
			}

			if id == "" {
				id = generateToolUseID()
			}

			index, found := s.toolIndexByID[id]

			if !found {
				var err error

				index, err = s.startBlock(&ContentBlock{
					Type:   "tool_use",
					ID:     id,
					Name:   content.ToolCall.Name,
					Input:  map[string]any{},
					Caller: &BlockCaller{Type: "direct"},
				})

				if err != nil {
					return err
				}

				s.toolIndexByID[id] = index
			}

			s.lastToolCallID = id

			if content.ToolCall.Arguments != "" {
				if err := s.emitEvent(StreamEvent{
					Type:  StreamEventContentBlockDelta,
					Index: index,
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

	// If no content was generated, send an empty text block
	if !s.hasContent {
		if _, err := s.startBlock(&ContentBlock{
			Type: "text",
			Text: ptr(""),
		}); err != nil {
			return err
		}
	}

	// Close all open content blocks
	for len(s.openBlocks) > 0 {
		if err := s.stopBlock(s.openBlocks[0]); err != nil {
			return err
		}
	}

	// Determine final stop reason from accumulated result
	if result.Message != nil {
		s.stopReason = toStopReason(result)
	}

	// Get final usage from accumulated result (prefer accumulated result over tracked values)
	inputTokens := s.inputTokens
	outputTokens := s.outputTokens
	var cacheReadInputTokens, cacheCreationInputTokens int
	var outputTokensDetails *OutputTokensDetails
	if result.Usage != nil {
		if resultInputTokens := anthropicInputTokens(result.Usage); resultInputTokens > inputTokens {
			inputTokens = resultInputTokens
		}
		if result.Usage.OutputTokens > outputTokens {
			outputTokens = result.Usage.OutputTokens
		}
		cacheReadInputTokens = result.Usage.CacheReadInputTokens
		cacheCreationInputTokens = result.Usage.CacheCreationInputTokens

		if result.Usage.ReasoningTokens > 0 || s.ThinkingEnabled {
			outputTokensDetails = &OutputTokensDetails{
				ThinkingTokens: result.Usage.ReasoningTokens,
			}
		}
	}

	// Send message_delta with stop_reason and usage
	stopDetails := (*StopDetails)(nil)
	if s.stopReason == StopReasonRefusal {
		stopDetails = &StopDetails{Type: "refusal"}

		if result.StopDetails != nil {
			stopDetails.Category = result.StopDetails.Category
			stopDetails.Explanation = result.StopDetails.Explanation
		}
	}

	messageDelta := &MessageDelta{
		StopReason:  s.stopReason,
		StopDetails: stopDetails,
	}

	if s.stopReason == StopReasonStopSequence {
		messageDelta.StopSequence = &result.StopSequence
	}

	if err := s.emitEvent(StreamEvent{
		Type:         StreamEventMessageDelta,
		MessageDelta: messageDelta,
		DeltaUsage: &DeltaUsage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,

			OutputTokensDetails: outputTokensDetails,

			CacheReadInputTokens:     cacheReadInputTokens,
			CacheCreationInputTokens: cacheCreationInputTokens,
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
			Type:    errorTypeForStatus(provider.CodeFromError(err, 0)),
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
