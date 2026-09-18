package responses

import (
	"encoding/json"
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
	StreamEventResponseError      StreamEventType = "response.error"
	StreamEventOutputItemAdded    StreamEventType = "output_item.added"
	StreamEventOutputItemDone     StreamEventType = "output_item.done"
	StreamEventContentPartAdded   StreamEventType = "content_part.added"
	StreamEventContentPartDone    StreamEventType = "content_part.done"
	StreamEventTextDelta          StreamEventType = "text.delta"
	StreamEventTextDone           StreamEventType = "text.done"

	// Refusal events
	StreamEventRefusalContentPartAdded StreamEventType = "refusal_content_part.added"
	StreamEventRefusalContentPartDone  StreamEventType = "refusal_content_part.done"
	StreamEventRefusalDelta            StreamEventType = "refusal.delta"
	StreamEventRefusalDone             StreamEventType = "refusal.done"

	// Function call events
	StreamEventFunctionCallAdded          StreamEventType = "function_call.added"
	StreamEventFunctionCallArgumentsDelta StreamEventType = "function_call_arguments.delta"
	StreamEventFunctionCallArgumentsDone  StreamEventType = "function_call_arguments.done"
	StreamEventFunctionCallDone           StreamEventType = "function_call.done"
	StreamEventToolSearchResult           StreamEventType = "tool_search.result"

	StreamEventCustomToolCallInputDelta StreamEventType = "custom_tool_call_input.delta"
	StreamEventCustomToolCallInputDone  StreamEventType = "custom_tool_call_input.done"

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

	// Compaction events
	StreamEventCompactionItemAdded StreamEventType = "compaction_item.added"
	StreamEventCompactionItemDone  StreamEventType = "compaction_item.done"
)

// StreamEvent represents a streaming event with its data
type StreamEvent struct {
	Type StreamEventType

	// For text delta events
	Delta string

	// For completion/done events - the full accumulated text
	Text string

	// For refusal events
	RefusalText string

	// For function call events
	ToolCallID        string
	ToolCallName      string
	ToolCallNamespace string
	ToolCallExecution string
	ToolCallAsync     bool
	ToolResult        *provider.ToolResult
	Arguments         string
	OutputIndex       int
	MessageID         string
	MessagePhase      provider.MessagePhase
	MessageIndex      int

	// Incomplete marks an item or summary part finalized before generation
	// finished (e.g. truncated by max_tokens or a cancelled stream).
	Incomplete bool

	// For reasoning events
	ReasoningID        string
	ReasoningText      string
	ReasoningSummary   string
	ReasoningSignature string
	SummaryIndex       int
	ContentIndex       int

	// For compaction events
	CompactionID               string
	CompactionContent          string
	CompactionEncryptedContent string

	// For error events
	Error error

	// The accumulated completion state
	Completion *provider.Completion
}

// StreamEventHandler is called for each streaming event
type StreamEventHandler func(event StreamEvent) error

// accumulatedToolCall holds per-tool-call state during streaming.
type accumulatedToolCall struct {
	ID string // call ID (e.g. call_xxx)

	Kind        provider.ToolKind
	Name        string
	Namespace   string
	Execution   string
	Async       bool
	Arguments   string
	OutputIndex int
	Started     bool
	Closed      bool // arguments.done + output_item.done already emitted
}

// StreamingAccumulator accumulates streaming completion chunks and emits
// Responses API SSE events. It is self-contained — there is no separate
// inner accumulator that could get out of sync.
type StreamingAccumulator struct {
	handler StreamEventHandler

	// When true, incoming Reasoning.Text is emitted as summary events instead of text events.
	ReasoningAsSummary bool

	// When true, reasoning output is silently dropped (client didn't request reasoning).
	SuppressReasoning bool

	// Completion metadata (captured from chunks)
	id               string
	model            string
	reasoningContext provider.ReasoningContext
	status           provider.CompletionStatus
	usage            *provider.Usage
	started          bool
	message          *streamMessage
	messages         []*streamMessage

	// Tool call state — single source of truth
	toolCalls      []accumulatedToolCall
	toolResults    []provider.ToolResult
	toolCallByID   map[string]int // effective call ID → index in toolCalls
	lastToolCallID string

	// In-flight reasoning state. Closed items archive into completedReasonings
	// before a new ID starts so each item's ID stays paired with its own
	// encrypted_content (otherwise OpenAI rejects the next turn).
	reasoningID              string
	reasoningSignature       string
	hasReasoningItem         bool
	hasReasoningTextPart     bool
	hasReasoningSummaryPart  bool
	reasoningOutputIndex     int
	reasoningClosed          bool
	streamedReasoningText    strings.Builder
	streamedReasoningSummary strings.Builder

	completedReasonings []provider.Reasoning

	compactionID        string
	compactionText      string
	compactionEncrypted string
	hasCompactionItem   bool
	compactionIndex     int

	compactions []provider.Compaction

	// Register every item when its output index is assigned so snapshots
	// retain the stream's order, including reasoning after a pause.
	outputOrder []streamItemRef
}

type streamMessage struct {
	id                 string
	phase              provider.MessagePhase
	hasOutputItem      bool
	hasContentPart     bool
	hasRefusalPart     bool
	messageClosed      bool
	messageOutputIndex int
	streamedText       strings.Builder
	streamedRefusal    strings.Builder
}

type streamItemKind int

const (
	streamItemMessage streamItemKind = iota
	streamItemReasoning
	streamItemCompaction
	streamItemToolCall
	streamItemToolResult
)

type streamItemRef struct {
	kind  streamItemKind
	index int
}

// NewStreamingAccumulator creates a new StreamingAccumulator with an event handler
func NewStreamingAccumulator(handler StreamEventHandler) *StreamingAccumulator {
	message := &streamMessage{}
	return &StreamingAccumulator{handler: handler, toolCallByID: make(map[string]int), message: message, messages: []*streamMessage{message}}
}

func mergeUsage(dst **provider.Usage, src *provider.Usage) {
	if src == nil {
		return
	}
	if *dst == nil {
		*dst = &provider.Usage{}
	}

	if src.InputTokens > (*dst).InputTokens {
		(*dst).InputTokens = src.InputTokens
	}
	if src.OutputTokens > (*dst).OutputTokens {
		(*dst).OutputTokens = src.OutputTokens
	}
	if tokens := src.ReasoningTokens; tokens != nil && ((*dst).ReasoningTokens == nil || *tokens > *(*dst).ReasoningTokens) {
		(*dst).ReasoningTokens = new(*tokens)
	}
	if src.CacheReadInputTokens > (*dst).CacheReadInputTokens {
		(*dst).CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.CacheCreationInputTokens > (*dst).CacheCreationInputTokens {
		(*dst).CacheCreationInputTokens = src.CacheCreationInputTokens
	}
}

func (s *StreamingAccumulator) reserveOutputIndex(kind streamItemKind, index int) int {
	idx := len(s.outputOrder)
	s.outputOrder = append(s.outputOrder, streamItemRef{kind: kind, index: index})
	return idx
}

func (s *StreamingAccumulator) start() error {
	if s.started {
		return nil
	}

	s.started = true

	if err := s.emitEvent(StreamEvent{Type: StreamEventResponseCreated, Completion: s.Result()}); err != nil {
		return err
	}

	return s.emitEvent(StreamEvent{Type: StreamEventResponseInProgress})
}

func (s *StreamingAccumulator) ensureMessageItem() error {
	if s.message.hasOutputItem {
		return nil
	}

	if err := s.closeCompaction(); err != nil {
		return err
	}

	s.message.hasOutputItem = true
	s.message.messageOutputIndex = s.reserveOutputIndex(streamItemMessage, len(s.messages)-1)

	return s.emitEvent(StreamEvent{
		Type:         StreamEventOutputItemAdded,
		OutputIndex:  s.message.messageOutputIndex,
		MessagePhase: s.message.phase,
	})
}

func (s *StreamingAccumulator) ensureMessageContentPart() error {
	if s.message.hasContentPart {
		return nil
	}

	s.message.hasContentPart = true

	return s.emitEvent(StreamEvent{
		Type:        StreamEventContentPartAdded,
		OutputIndex: s.message.messageOutputIndex,
	})
}

func (s *StreamingAccumulator) ensureMessageRefusalPart() error {
	if s.message.hasRefusalPart {
		return nil
	}

	s.message.hasRefusalPart = true

	return s.emitEvent(StreamEvent{
		Type:         StreamEventRefusalContentPartAdded,
		OutputIndex:  s.message.messageOutputIndex,
		ContentIndex: s.refusalContentIndex(),
	})
}

// refusalContentIndex places the refusal part after the text part when the
// message carries both.
func (s *StreamingAccumulator) refusalContentIndex() int {
	if s.message.hasContentPart || s.message.streamedText.Len() > 0 {
		return 1
	}

	return 0
}

// trackToolCall ensures a tool call entry exists and returns its effective ID,
// output index, and whether tracking succeeded.
func (s *StreamingAccumulator) trackToolCall(toolCall provider.ToolCall) (string, int, bool) {
	effectiveID := toolCall.ID

	if effectiveID != "" {
		if _, exists := s.toolCallByID[effectiveID]; !exists {
			idx := len(s.toolCalls)
			s.toolCalls = append(s.toolCalls, accumulatedToolCall{
				ID:          effectiveID,
				OutputIndex: s.reserveOutputIndex(streamItemToolCall, idx),
			})
			s.toolCallByID[effectiveID] = idx
		}
		s.lastToolCallID = effectiveID
	}

	currentID := effectiveID
	if currentID == "" {
		currentID = s.lastToolCallID
	}

	if currentID == "" {
		return "", 0, false
	}

	tc := &s.toolCalls[s.toolCallByID[currentID]]
	return currentID, tc.OutputIndex, true
}

func (s *StreamingAccumulator) ensureToolCallStarted(callID string, toolCall provider.ToolCall, outputIndex int) error {
	tc := &s.toolCalls[s.toolCallByID[callID]]
	if tc.Started {
		return nil
	}

	tc.Started = true

	if toolCall.Name != "" {
		tc.Name = toolCall.Name
	}

	if toolCall.Namespace != "" {
		tc.Namespace = toolCall.Namespace
	}

	if toolCall.Execution != "" {
		tc.Execution = toolCall.Execution
	}
	if toolCall.Async {
		tc.Async = true
	}

	return s.emitEvent(StreamEvent{
		Type:              StreamEventFunctionCallAdded,
		ToolCallID:        callID,
		ToolCallName:      tc.Name,
		ToolCallNamespace: tc.Namespace,
		ToolCallExecution: tc.Execution,
		ToolCallAsync:     tc.Async,
		OutputIndex:       outputIndex,
	})
}

// closeToolCall emits output_item.done for the given call. A call whose
// arguments are complete also gets an arguments.done; an incomplete one
// (truncated/cancelled mid-arguments) is finalized as incomplete and skips
// arguments.done, since claiming "done" with truncated JSON is a lie.
func (s *StreamingAccumulator) closeToolCall(callID string) error {
	idx, ok := s.toolCallByID[callID]
	if !ok {
		return nil
	}

	tc := &s.toolCalls[idx]
	if !tc.Started || tc.Closed {
		return nil
	}

	tc.Closed = true

	incomplete := s.toolCallIncomplete(callID)

	// A function call that streamed no argument bytes must still deliver
	// parseable arguments — surface the canonical empty object.
	if !incomplete && tc.Arguments == "" && tc.Kind != provider.ToolKindCustom {
		tc.Arguments = "{}"

		if err := s.emitEvent(StreamEvent{
			Type:              StreamEventFunctionCallArgumentsDelta,
			ToolCallID:        tc.ID,
			ToolCallName:      tc.Name,
			ToolCallNamespace: tc.Namespace,
			Delta:             tc.Arguments,
			OutputIndex:       tc.OutputIndex,
		}); err != nil {
			return err
		}
	}

	if !incomplete {
		if err := s.emitEvent(StreamEvent{
			Type:              StreamEventFunctionCallArgumentsDone,
			ToolCallID:        tc.ID,
			ToolCallName:      tc.Name,
			ToolCallNamespace: tc.Namespace,
			ToolCallExecution: tc.Execution,
			ToolCallAsync:     tc.Async,
			Arguments:         tc.Arguments,
			OutputIndex:       tc.OutputIndex,
		}); err != nil {
			return err
		}
	}

	return s.emitEvent(StreamEvent{
		Type:              StreamEventFunctionCallDone,
		ToolCallID:        tc.ID,
		ToolCallName:      tc.Name,
		ToolCallNamespace: tc.Namespace,
		ToolCallExecution: tc.Execution,
		ToolCallAsync:     tc.Async,
		Arguments:         tc.Arguments,
		OutputIndex:       tc.OutputIndex,
		Incomplete:        incomplete,
	})
}

// toolCallIncomplete reports a filtered call or missing/incomplete arguments
// in a truncated response. A normally completed response never marks a call
// incomplete, including custom tools whose input isn't JSON.
func (s *StreamingAccumulator) toolCallIncomplete(callID string) bool {
	if s.contentFiltered() {
		return true
	}

	if s.status != provider.CompletionStatusIncomplete {
		return false
	}

	idx, ok := s.toolCallByID[callID]
	if !ok {
		return false
	}

	args := strings.TrimSpace(s.toolCalls[idx].Arguments)

	return args == "" || !json.Valid([]byte(args))
}

func (s *StreamingAccumulator) toolCallArgumentsComplete(callID string) bool {
	idx, ok := s.toolCallByID[callID]
	if !ok {
		return false
	}

	args := strings.TrimSpace(s.toolCalls[idx].Arguments)
	return args != "" && json.Valid([]byte(args))
}

func (s *StreamingAccumulator) shouldClosePreviousToolCall(nextID string) bool {
	return nextID != "" &&
		nextID != s.lastToolCallID &&
		s.lastToolCallID != "" &&
		s.toolCallArgumentsComplete(s.lastToolCallID)
}

func (s *StreamingAccumulator) ensureReasoningItem() error {
	if s.hasReasoningItem {
		return nil
	}

	if err := s.closeCompaction(); err != nil {
		return err
	}

	s.hasReasoningItem = true
	s.reasoningOutputIndex = s.reserveOutputIndex(streamItemReasoning, len(s.completedReasonings))

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

// closeReasoning emits all the "done" events for reasoning if it was in progress.
// On success it archives the reasoning state into completedReasonings and
// resets the in-flight fields, so a subsequent reasoning item with a new ID
// can be started cleanly.
func (s *StreamingAccumulator) closeReasoning() error {
	if !s.hasReasoningItem || s.reasoningClosed {
		return nil
	}
	s.reasoningClosed = true

	reasoningText := s.streamedReasoningText.String()
	reasoningSummary := s.streamedReasoningSummary.String()
	incomplete := s.status == provider.CompletionStatusIncomplete || s.contentFiltered()

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

		if err := s.emitEvent(StreamEvent{
			Type:             StreamEventReasoningSummaryPartDone,
			ReasoningID:      s.reasoningID,
			ReasoningSummary: reasoningSummary,
			OutputIndex:      s.reasoningOutputIndex,
			SummaryIndex:     0,
			Incomplete:       incomplete,
		}); err != nil {
			return err
		}
	}

	if err := s.emitEvent(StreamEvent{
		Type:               StreamEventReasoningItemDone,
		ReasoningID:        s.reasoningID,
		ReasoningText:      reasoningText,
		ReasoningSummary:   reasoningSummary,
		ReasoningSignature: s.reasoningSignature,
		OutputIndex:        s.reasoningOutputIndex,
		Incomplete:         incomplete,
	}); err != nil {
		return err
	}

	s.completedReasonings = append(s.completedReasonings, provider.Reasoning{
		ID:        s.reasoningID,
		Text:      reasoningText,
		Summary:   reasoningSummary,
		Signature: s.reasoningSignature,
	})

	s.reasoningID = ""
	s.reasoningSignature = ""
	s.hasReasoningItem = false
	s.hasReasoningTextPart = false
	s.hasReasoningSummaryPart = false
	s.reasoningClosed = false
	s.streamedReasoningText.Reset()
	s.streamedReasoningSummary.Reset()

	return nil
}

// closeMessage emits done events for the message item if it was in progress.
func (s *StreamingAccumulator) closeMessage() error {
	if !s.message.hasOutputItem || s.message.messageClosed {
		return nil
	}

	if s.message.streamedText.Len() == 0 && s.message.streamedRefusal.Len() == 0 {
		return nil
	}

	s.message.messageClosed = true
	text := s.message.streamedText.String()
	refusal := s.message.streamedRefusal.String()

	if s.message.streamedText.Len() > 0 {
		if err := s.emitEvent(StreamEvent{
			Type:        StreamEventTextDone,
			Text:        text,
			OutputIndex: s.message.messageOutputIndex,
		}); err != nil {
			return err
		}

		if err := s.emitEvent(StreamEvent{
			Type:        StreamEventContentPartDone,
			Text:        text,
			OutputIndex: s.message.messageOutputIndex,
		}); err != nil {
			return err
		}
	}

	if s.message.streamedRefusal.Len() > 0 {
		if err := s.emitEvent(StreamEvent{
			Type:         StreamEventRefusalDone,
			RefusalText:  refusal,
			OutputIndex:  s.message.messageOutputIndex,
			ContentIndex: s.refusalContentIndex(),
		}); err != nil {
			return err
		}

		if err := s.emitEvent(StreamEvent{
			Type:         StreamEventRefusalContentPartDone,
			RefusalText:  refusal,
			OutputIndex:  s.message.messageOutputIndex,
			ContentIndex: s.refusalContentIndex(),
		}); err != nil {
			return err
		}
	}

	return s.emitEvent(StreamEvent{
		Type:         StreamEventOutputItemDone,
		Text:         text,
		RefusalText:  refusal,
		OutputIndex:  s.message.messageOutputIndex,
		Incomplete:   s.status == provider.CompletionStatusIncomplete || s.contentFiltered(),
		MessagePhase: s.message.phase,
	})
}

// closePendingItems closes any in-progress compaction, reasoning and message
// items. Must be called before emitting function call events so that the
// client sees each output item completed before the next one starts.
func (s *StreamingAccumulator) closePendingItems() error {
	if err := s.closeCompaction(); err != nil {
		return err
	}

	if err := s.closeReasoning(); err != nil {
		return err
	}

	return s.closeMessage()
}

func (s *StreamingAccumulator) closeCompaction() error {
	if !s.hasCompactionItem {
		return nil
	}

	s.compactions = append(s.compactions, provider.Compaction{
		ID:        s.compactionID,
		Content:   s.compactionText,
		Signature: s.compactionEncrypted,
	})

	err := s.emitEvent(StreamEvent{
		Type:                       StreamEventCompactionItemDone,
		CompactionID:               s.compactionID,
		CompactionContent:          s.compactionText,
		CompactionEncryptedContent: s.compactionEncrypted,
		OutputIndex:                s.compactionIndex,
	})

	s.hasCompactionItem = false
	s.compactionID = ""
	s.compactionText = ""
	s.compactionEncrypted = ""

	return err
}

// Add processes a completion chunk and emits appropriate events.
func (s *StreamingAccumulator) Add(c provider.Completion) error {
	// Capture metadata
	if c.ID != "" {
		s.id = c.ID
	}
	if c.Model != "" {
		s.model = c.Model
	}
	if c.Reasoning != "" {
		s.reasoningContext = c.Reasoning
	}
	if c.Status != "" {
		s.status = c.Status
	}
	if c.Usage != nil {
		mergeUsage(&s.usage, c.Usage)
	}

	if err := s.start(); err != nil {
		return err
	}

	if c.Message == nil {
		return nil
	}
	// Identified items carry their own phase; a message-level phase describes
	// the enclosing, unidentified message.
	if c.Message.Phase != "" && s.message.id == "" {
		s.message.phase = c.Message.Phase
	}

	for _, content := range c.Message.Content {
		if content.MessageID != "" {
			if err := s.beginMessage(content.MessageID, content.Phase); err != nil {
				return err
			}
		}

		// Compaction may stream in chunks; the item stays in-flight until
		// other content starts or the stream completes.
		if content.Compaction != nil && (content.Compaction.Content != "" || content.Compaction.Signature != "") {
			if s.hasCompactionItem && content.Compaction.ID != "" && s.compactionID != "" && content.Compaction.ID != s.compactionID {
				if err := s.closeCompaction(); err != nil {
					return err
				}
			}

			if !s.hasCompactionItem {
				if err := s.closePendingItems(); err != nil {
					return err
				}

				s.hasCompactionItem = true
				s.compactionIndex = s.reserveOutputIndex(streamItemCompaction, len(s.compactions))

				s.compactionID = content.Compaction.ID
				if s.compactionID == "" {
					s.compactionID = "comp_" + uuid.NewString()
				}

				if err := s.emitEvent(StreamEvent{
					Type:         StreamEventCompactionItemAdded,
					CompactionID: s.compactionID,
					OutputIndex:  s.compactionIndex,
				}); err != nil {
					return err
				}
			}

			if content.Compaction.Content != "" {
				s.compactionText = content.Compaction.Content
			}

			if content.Compaction.Signature != "" {
				s.compactionEncrypted = content.Compaction.Signature
			}
		}

		// Reasoning can resume after text when a paused turn continues.
		if content.Reasoning != nil && !s.SuppressReasoning {
			r := content.Reasoning

			// A new ID means this delta belongs to a different reasoning item.
			// Close the in-flight one before starting the new one so each item
			// gets its own output_item.done with its own encrypted_content.
			if r.ID != "" && s.hasReasoningItem && s.reasoningID != "" && r.ID != s.reasoningID {
				if err := s.closeReasoning(); err != nil {
					return err
				}
			}

			if r.ID != "" && s.reasoningID == "" {
				s.reasoningID = r.ID
			}

			if r.ID != "" {
				if err := s.ensureReasoningItem(); err != nil {
					return err
				}
			}

			if r.Signature != "" {
				s.reasoningSignature = r.Signature

				if err := s.ensureReasoningItem(); err != nil {
					return err
				}
			}

			if r.Text != "" && s.ReasoningAsSummary {
				// Redirect reasoning text to summary events when summary mode is active
				r.Summary = r.Text
				r.Text = ""
			}

			if r.Text != "" {
				if err := s.ensureReasoningItem(); err != nil {
					return err
				}
				if err := s.ensureReasoningTextPart(); err != nil {
					return err
				}

				s.streamedReasoningText.WriteString(r.Text)

				if err := s.emitEvent(StreamEvent{
					Type:         StreamEventReasoningTextDelta,
					ReasoningID:  s.reasoningID,
					Delta:        r.Text,
					OutputIndex:  s.reasoningOutputIndex,
					ContentIndex: 0,
				}); err != nil {
					return err
				}
			}

			if r.Summary != "" {
				if err := s.ensureReasoningItem(); err != nil {
					return err
				}
				if err := s.ensureReasoningSummaryPart(); err != nil {
					return err
				}

				s.streamedReasoningSummary.WriteString(r.Summary)

				if err := s.emitEvent(StreamEvent{
					Type:         StreamEventReasoningSummaryDelta,
					ReasoningID:  s.reasoningID,
					Delta:        r.Summary,
					OutputIndex:  s.reasoningOutputIndex,
					SummaryIndex: 0,
				}); err != nil {
					return err
				}
			}
		}

		if content.Refusal != "" {
			if err := s.closeCompaction(); err != nil {
				return err
			}

			if s.message.id != "" && s.message.streamedText.Len() > 0 {
				if err := s.nextMessage(s.message.id, s.message.phase); err != nil {
					return err
				}
			}

			s.message.streamedRefusal.WriteString(content.Refusal)

			if len(s.toolCalls) == 0 {
				if err := s.closeReasoning(); err != nil {
					return err
				}

				if err := s.ensureMessageItem(); err != nil {
					return err
				}
				if err := s.ensureMessageRefusalPart(); err != nil {
					return err
				}

				if err := s.emitEvent(StreamEvent{
					Type:         StreamEventRefusalDelta,
					Delta:        content.Refusal,
					OutputIndex:  s.message.messageOutputIndex,
					ContentIndex: s.refusalContentIndex(),
				}); err != nil {
					return err
				}
			}
		}

		// Text — only emit if no tool calls have started yet; otherwise
		// just accumulate (will be flushed in Complete). This prevents
		// opening a message output item after function_call items when
		// an upstream provider sends text after tool calls.
		if content.Text != "" {
			if err := s.closeCompaction(); err != nil {
				return err
			}

			if s.message.id != "" && s.message.streamedRefusal.Len() > 0 {
				if err := s.nextMessage(s.message.id, s.message.phase); err != nil {
					return err
				}
			}

			s.message.streamedText.WriteString(content.Text)

			if len(s.toolCalls) == 0 {
				if err := s.closeReasoning(); err != nil {
					return err
				}

				if err := s.ensureMessageItem(); err != nil {
					return err
				}
				if err := s.ensureMessageContentPart(); err != nil {
					return err
				}

				if err := s.emitEvent(StreamEvent{
					Type:        StreamEventTextDelta,
					Delta:       content.Text,
					OutputIndex: s.message.messageOutputIndex,
				}); err != nil {
					return err
				}
			}
		}

		if content.ToolResult != nil && content.ToolResult.Kind == provider.ToolKindToolSearch {
			if err := s.closePendingItems(); err != nil {
				return err
			}
			if err := s.closeToolCall(content.ToolResult.ID); err != nil {
				return err
			}
			index := s.reserveOutputIndex(streamItemToolResult, len(s.toolResults))
			s.toolResults = append(s.toolResults, *content.ToolResult)
			if err := s.emitEvent(StreamEvent{Type: StreamEventToolSearchResult, OutputIndex: index, ToolResult: content.ToolResult}); err != nil {
				return err
			}
		}

		// Tool calls — close pending reasoning and message before starting
		if content.ToolCall != nil {
			tc := content.ToolCall

			if err := s.closePendingItems(); err != nil {
				return err
			}

			// If this chunk introduces a new tool call ID and the previous
			// call's arguments are complete, close the previous item before
			// starting the next one. If the JSON is still incomplete, leave it
			// open so later fragments cannot arrive after arguments.done.
			if s.shouldClosePreviousToolCall(tc.ID) {
				if err := s.closeToolCall(s.lastToolCallID); err != nil {
					return err
				}
			}

			currentID, outputIndex, ok := s.trackToolCall(*tc)
			if !ok {
				continue
			}

			if err := s.ensureToolCallStarted(currentID, *tc, outputIndex); err != nil {
				return err
			}

			// Accumulate name and arguments
			entry := &s.toolCalls[s.toolCallByID[currentID]]

			if tc.Kind != "" {
				entry.Kind = tc.Kind
			}

			if tc.Name != "" {
				entry.Name = tc.Name
			}

			if tc.Namespace != "" {
				entry.Namespace = tc.Namespace
			}

			if tc.Execution != "" {
				entry.Execution = tc.Execution
			}
			if tc.Async {
				entry.Async = true
			}

			if tc.Arguments != "" {
				entry.Arguments += tc.Arguments

				if err := s.emitEvent(StreamEvent{
					Type:              StreamEventFunctionCallArgumentsDelta,
					ToolCallID:        currentID,
					ToolCallName:      entry.Name,
					ToolCallNamespace: entry.Namespace,
					Delta:             tc.Arguments,
					OutputIndex:       outputIndex,
				}); err != nil {
					return err
				}
			}
		}

	}

	return nil
}

// Complete signals that streaming is done and emits final events.
func (s *StreamingAccumulator) Complete() error {
	if err := s.start(); err != nil {
		return err
	}

	if err := s.closeCompaction(); err != nil {
		return err
	}

	if err := s.flushMessage(); err != nil {
		return err
	}

	result := s.Result()
	terminalType := StreamEventResponseCompleted
	if s.status == provider.CompletionStatusIncomplete || s.contentFiltered() {
		terminalType = StreamEventResponseIncomplete
	}

	return s.emitEvent(StreamEvent{
		Type:       terminalType,
		Completion: result,
	})
}

// contentFiltered reports a refusal that arrived as ordinary text. Native stop
// reasons such as Anthropic's refusal or a Bedrock guardrail halt the output
// after text was streamed, without a refusal part. OpenAI expresses that as
// an incomplete response with the content_filter reason, which clients
// already treat as a turn that must not continue.
func contentFiltered(c *provider.Completion) bool {
	if c == nil || c.Status != provider.CompletionStatusRefused {
		return false
	}
	return c.Message == nil || c.Message.Refusal() == ""
}

func (s *StreamingAccumulator) contentFiltered() bool {
	if s.status != provider.CompletionStatusRefused {
		return false
	}
	for _, message := range s.messages {
		if message.streamedRefusal.Len() > 0 {
			return false
		}
	}
	return true
}

// beginMessage makes the item with the given ID current. A new ID starts an
// item, reusing the current one while it holds no text or refusal; ID-less
// deltas remain in the current item. Phase is item metadata and may be empty.
func (s *StreamingAccumulator) beginMessage(id string, phase provider.MessagePhase) error {
	if s.message.id == id {
		if phase != "" {
			s.message.phase = phase
		}

		return nil
	}

	if s.message.streamedText.Len() > 0 || s.message.streamedRefusal.Len() > 0 {
		return s.nextMessage(id, phase)
	}

	s.message.id = id
	s.message.phase = phase

	return nil
}

// nextMessage closes the current message item and starts a new one. Identified
// items keep text and refusals separate: the text and refusal branches call it
// with the same ID, and SplitMessages splits such items the same way.
func (s *StreamingAccumulator) nextMessage(id string, phase provider.MessagePhase) error {
	if err := s.flushMessage(); err != nil {
		return err
	}

	s.message = &streamMessage{id: id, phase: phase}
	s.messages = append(s.messages, s.message)

	return nil
}

func (s *StreamingAccumulator) flushMessage() error {
	text := s.message.streamedText.String()

	if err := s.closeReasoning(); err != nil {
		return err
	}

	// Close open tool calls before flushing withheld trailing text: items must
	// stream strictly sequentially, and the message item's output index (assigned
	// below) follows the calls it trailed. closeToolCall is idempotent, so calls
	// already closed inline (when a later call started) are no-ops here.
	for i := range s.toolCalls {
		if err := s.closeToolCall(s.toolCalls[i].ID); err != nil {
			return err
		}
	}

	if (s.message.streamedText.Len() > 0 || s.message.streamedRefusal.Len() > 0) && !s.message.hasOutputItem {
		if err := s.ensureMessageItem(); err != nil {
			return err
		}

		if s.message.streamedText.Len() > 0 {
			if err := s.emitEvent(StreamEvent{
				Type:        StreamEventContentPartAdded,
				OutputIndex: s.message.messageOutputIndex,
			}); err != nil {
				return err
			}
			s.message.hasContentPart = true

			if err := s.emitEvent(StreamEvent{
				Type:        StreamEventTextDelta,
				Delta:       text,
				OutputIndex: s.message.messageOutputIndex,
			}); err != nil {
				return err
			}
		}

		if s.message.streamedRefusal.Len() > 0 {
			if err := s.emitEvent(StreamEvent{
				Type:         StreamEventRefusalContentPartAdded,
				OutputIndex:  s.message.messageOutputIndex,
				ContentIndex: s.refusalContentIndex(),
			}); err != nil {
				return err
			}
			s.message.hasRefusalPart = true

			if err := s.emitEvent(StreamEvent{
				Type:         StreamEventRefusalDelta,
				Delta:        s.message.streamedRefusal.String(),
				OutputIndex:  s.message.messageOutputIndex,
				ContentIndex: s.refusalContentIndex(),
			}); err != nil {
				return err
			}
		}
	}

	if err := s.closeMessage(); err != nil {
		return err
	}

	return nil
}

func (s *StreamingAccumulator) Error(err error) error {
	_ = s.start()

	if e := s.emitEvent(StreamEvent{
		Type:  StreamEventResponseError,
		Error: err,
	}); e != nil {
		return e
	}

	return s.emitEvent(StreamEvent{
		Type:  StreamEventResponseFailed,
		Error: err,
	})
}

// Result builds the accumulated completion from the accumulator's own state.
func (s *StreamingAccumulator) Result() *provider.Completion {
	var content []provider.Content

	appendMessage := func(m *streamMessage) {
		if m.streamedText.Len() > 0 {
			content = append(content, provider.Content{MessageID: m.id, Phase: m.phase, Text: m.streamedText.String()})
		}
		if m.streamedRefusal.Len() > 0 {
			content = append(content, provider.Content{MessageID: m.id, Phase: m.phase, Refusal: m.streamedRefusal.String()})
		}
	}

	for _, ref := range s.outputOrder {
		switch ref.kind {
		case streamItemMessage:
			appendMessage(s.messages[ref.index])

		case streamItemReasoning:
			if ref.index < len(s.completedReasonings) {
				content = append(content, provider.ReasoningContent(s.completedReasonings[ref.index]))
			} else {
				content = append(content, provider.ReasoningContent(provider.Reasoning{
					ID:        s.reasoningID,
					Text:      s.streamedReasoningText.String(),
					Summary:   s.streamedReasoningSummary.String(),
					Signature: s.reasoningSignature,
				}))
			}

		case streamItemCompaction:
			if ref.index < len(s.compactions) {
				content = append(content, provider.CompactionContent(s.compactions[ref.index]))
			} else {
				content = append(content, provider.CompactionContent(provider.Compaction{
					ID:        s.compactionID,
					Content:   s.compactionText,
					Signature: s.compactionEncrypted,
				}))
			}

		case streamItemToolCall:
			tc := &s.toolCalls[ref.index]
			call := provider.ToolCall{
				ID:        tc.ID,
				Async:     tc.Async,
				Kind:      tc.Kind,
				Name:      tc.Name,
				Namespace: tc.Namespace,
				Execution: tc.Execution,
				Arguments: tc.Arguments,
			}

			// A truncated call keeps its empty arguments — fabricating "{}"
			// would disguise an aborted call as a valid zero-argument one.
			if s.status != provider.CompletionStatusIncomplete && !s.contentFiltered() {
				call = provider.NormalizeToolCallArguments(call)
			}

			content = append(content, provider.ToolCallContent(call))
		case streamItemToolResult:
			content = append(content, provider.ToolResultContent(s.toolResults[ref.index]))
		}
	}

	// Before Complete, trailing text may still be waiting for tool calls to
	// finish. Include it in partial snapshots without assigning an index early.
	if !s.message.hasOutputItem {
		appendMessage(s.message)
	}

	return &provider.Completion{
		ID:        s.id,
		Model:     s.model,
		Status:    s.status,
		Usage:     s.usage,
		Reasoning: s.reasoningContext,

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Phase:   s.message.phase,
			Content: content,
		},
	}
}

func (s *StreamingAccumulator) emitEvent(event StreamEvent) error {
	event.MessageIndex = len(s.messages) - 1
	event.MessageID = s.message.id
	if s.handler != nil {
		return s.handler(event)
	}
	return nil
}
