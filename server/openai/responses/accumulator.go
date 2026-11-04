package responses

import (
	"encoding/json"
	"fmt"
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
	RefusalText  string
	RefusalFirst bool

	// For function call events
	ToolCallID        string
	ToolCallKind      provider.ToolKind
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
	TextPresent       bool
	// Terminal snapshots preserve the status already emitted for each item.
	OutputStatuses []string

	// Incomplete marks an item or summary part finalized before generation
	// finished (e.g. truncated by max_tokens or a cancelled stream).
	Incomplete bool

	// For reasoning events
	ReasoningID           string
	ReasoningText         string
	ReasoningSummary      string
	ReasoningSignature    string
	ReasoningRedacted     bool
	ReasoningSummaryParts []provider.Part
	ReasoningContentParts []provider.Part
	SummaryIndex          int
	ContentIndex          int

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
	// Items are emitted in output order. Later items wait behind an unfinished
	// tool call, while that call's argument fragments continue streaming.
	nextEventIndex int
	pendingEvents  map[int][]StreamEvent
	handler        StreamEventHandler
	// When true, incoming Reasoning.Text is emitted as summary events instead of text events.
	ReasoningAsSummary bool
	// When true, reasoning output is silently dropped (client didn't request reasoning).
	SuppressReasoning bool

	// Completion metadata (captured from chunks)
	id    string
	model string

	tier             provider.Tier
	reasoningContext provider.ReasoningContext

	started      bool
	status       provider.CompletionStatus
	stopReason   provider.StopReason
	stopDetails  *provider.StopDetails
	stopSequence string

	message  *streamMessage
	messages []*streamMessage

	// Tool call state — single source of truth
	toolCalls      []accumulatedToolCall
	toolResults    []provider.ToolResult
	toolCallByID   map[string]int // effective call ID → index in toolCalls
	lastToolCallID string

	// In-flight reasoning state. Closed items archive into completedReasonings
	// before a new ID starts so each item's ID stays paired with its own
	// encrypted_content (otherwise OpenAI rejects the next turn).
	reasoningID          string
	reasoningSignature   string
	reasoningRedacted    bool
	reasoningOutputIndex int
	reasoningClosed      bool
	hasReasoningItem     bool

	reasoningContent    reasoningParts
	reasoningSummary    reasoningParts
	completedReasonings []provider.Reasoning

	compactionID        string
	compactionIndex     int
	compactionEncrypted string
	compactionText      string
	hasCompactionItem   bool
	compactions         []provider.Compaction

	// Register every item when its output index is assigned so snapshots
	// retain the stream's order, including reasoning after a message.
	outputOrder    []streamItemRef
	outputStatuses []string

	usage *provider.Usage
}

type streamMessage struct {
	id                 string
	phase              provider.MessagePhase
	messageOutputIndex int

	hasOutputItem  bool
	hasContentPart bool
	hasRefusalPart bool
	refusalFirst   bool
	messageClosed  bool

	streamedText    strings.Builder
	streamedRefusal strings.Builder
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
	(*dst).Merge(src)
}

func (s *StreamingAccumulator) reserveOutputIndex(kind streamItemKind, index int) int {
	idx := len(s.outputOrder)
	s.outputOrder = append(s.outputOrder, streamItemRef{kind: kind, index: index})
	s.outputStatuses = append(s.outputStatuses, "")
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
		Type:         StreamEventContentPartAdded,
		OutputIndex:  s.message.messageOutputIndex,
		ContentIndex: s.textContentIndex(),
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

func (s *StreamingAccumulator) textContentIndex() int {
	if s.message.refusalFirst {
		return 1
	}
	return 0
}

// Content indexes retain the order in which parts first arrived.
func (s *StreamingAccumulator) refusalContentIndex() int {
	if s.message.refusalFirst {
		return 0
	}
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

	if toolCall.Kind != "" {
		tc.Kind = toolCall.Kind
	}

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
		ToolCallKind:      tc.Kind,
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
			ToolCallKind:      tc.Kind,
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
			ToolCallKind:      tc.Kind,
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
		ToolCallKind:      tc.Kind,
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
	idx, ok := s.toolCallByID[callID]
	if !ok {
		return false
	}
	call := s.toolCalls[idx]
	// A hosted result has already completed this operation; a later refusal
	// concerns the remaining response, not a call the client could execute.
	if !callerOwned(call.Kind, call.Execution) {
		return false
	}
	if s.status == provider.CompletionStatusRefused || s.status == provider.CompletionStatusFailed {
		return true
	}

	if s.status != provider.CompletionStatusIncomplete {
		return false
	}

	args := strings.TrimSpace(call.Arguments)

	return args == "" || !json.Valid([]byte(args))
}

func (s *StreamingAccumulator) ensureReasoningItem() error {
	if s.hasReasoningItem {
		return nil
	}

	if err := s.closeCompaction(); err != nil {
		return err
	}

	if err := s.closeMessage(); err != nil {
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

// Reasoning content and summaries have separate index spaces. Buffer raw
// content in summary mode until a native summary appears or the item ends.
// This prevents a later summary from being mixed with a speculative fallback.
type reasoningParts struct {
	provider.PartAccumulator
	offsets []int
}

func (s *StreamingAccumulator) emitReasoningArray(state *reasoningParts, summary, finish bool) error {
	for index := 0; index < state.Len(); index++ {
		part := state.Part(index)
		// An unseen lower index may still arrive. Stream the contiguous prefix;
		// compact any remaining sparse indexes when the reasoning item ends.
		if !finish && part.Index != index {
			break
		}
		if index == len(state.offsets) {
			kind := StreamEventReasoningContentPartAdded
			if summary {
				kind = StreamEventReasoningSummaryPartAdded
			}
			if err := s.emitEvent(StreamEvent{Type: kind, ReasoningID: s.reasoningID, OutputIndex: s.reasoningOutputIndex, ContentIndex: index, SummaryIndex: index}); err != nil {
				return err
			}
			state.offsets = append(state.offsets, 0)
		}
		if offset := state.offsets[index]; offset < len(part.Text) {
			kind := StreamEventReasoningTextDelta
			if summary {
				kind = StreamEventReasoningSummaryDelta
			}
			if err := s.emitEvent(StreamEvent{Type: kind, ReasoningID: s.reasoningID, OutputIndex: s.reasoningOutputIndex, ContentIndex: index, SummaryIndex: index, Delta: part.Text[offset:]}); err != nil {
				return err
			}
			state.offsets[index] = len(part.Text)
		}
		// Keep parts open for later deltas at an earlier native index.
		if !finish {
			continue
		}
		kinds := []StreamEventType{StreamEventReasoningTextDone, StreamEventReasoningContentPartDone}
		if summary {
			kinds = []StreamEventType{StreamEventReasoningSummaryDone, StreamEventReasoningSummaryPartDone}
		}
		for _, kind := range kinds {
			if err := s.emitEvent(StreamEvent{Type: kind, ReasoningID: s.reasoningID, OutputIndex: s.reasoningOutputIndex, ContentIndex: index, SummaryIndex: index, ReasoningText: part.Text, ReasoningSummary: part.Text, Incomplete: s.itemIncomplete()}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *StreamingAccumulator) emitReasoningParts(finish bool) error {
	if s.ReasoningAsSummary && !s.reasoningSummary.HasText() {
		if !finish {
			return nil
		}
		if s.reasoningContent.HasText() {
			return s.emitReasoningArray(&s.reasoningContent, true, true)
		}
	}
	if err := s.emitReasoningArray(&s.reasoningContent, false, finish); err != nil {
		return err
	}
	return s.emitReasoningArray(&s.reasoningSummary, true, finish)
}

func (s *StreamingAccumulator) currentReasoning() provider.Reasoning {
	return provider.Reasoning{ID: s.reasoningID, Signature: s.reasoningSignature, Redacted: s.reasoningRedacted,
		Content: s.reasoningContent.Parts(), Summary: s.reasoningSummary.Parts()}
}

func (s *StreamingAccumulator) closeReasoning() error {
	if !s.hasReasoningItem || s.reasoningClosed {
		return nil
	}
	s.reasoningClosed = true
	if err := s.emitReasoningParts(true); err != nil {
		return err
	}
	native := s.currentReasoning()
	output := reasoningForOutput(native, s.ReasoningAsSummary)
	if err := s.emitEvent(StreamEvent{Type: StreamEventReasoningItemDone,
		ReasoningID: output.ID, ReasoningText: output.Text(), ReasoningSummary: output.SummaryText(),
		ReasoningContentParts: output.Content, ReasoningSummaryParts: output.Summary,
		ReasoningSignature: output.Signature, ReasoningRedacted: output.Redacted,
		OutputIndex: s.reasoningOutputIndex, Incomplete: s.itemIncomplete()}); err != nil {
		return err
	}
	s.completedReasonings = append(s.completedReasonings, native)
	s.reasoningID, s.reasoningSignature = "", ""
	s.reasoningRedacted, s.hasReasoningItem, s.reasoningClosed = false, false, false
	s.reasoningContent, s.reasoningSummary = reasoningParts{}, reasoningParts{}
	return nil
}

// closeMessage emits done events for the message item if it was in progress.
func (s *StreamingAccumulator) closeMessage() error {
	if !s.message.hasOutputItem || s.message.messageClosed {
		return nil
	}

	s.message.messageClosed = true
	text := s.message.streamedText.String()
	refusal := s.message.streamedRefusal.String()

	if s.message.hasContentPart {
		if err := s.emitEvent(StreamEvent{
			Type:         StreamEventTextDone,
			Text:         text,
			OutputIndex:  s.message.messageOutputIndex,
			ContentIndex: s.textContentIndex(),
		}); err != nil {
			return err
		}

		if err := s.emitEvent(StreamEvent{
			Type:         StreamEventContentPartDone,
			Text:         text,
			OutputIndex:  s.message.messageOutputIndex,
			ContentIndex: s.textContentIndex(),
		}); err != nil {
			return err
		}
	}

	if s.message.hasRefusalPart {
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
		TextPresent:  s.message.hasContentPart,
		Text:         text,
		RefusalText:  refusal,
		RefusalFirst: s.message.refusalFirst,
		OutputIndex:  s.message.messageOutputIndex,
		Incomplete:   s.itemIncomplete(),
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
	if c.Tier != "" {
		s.tier = c.Tier
	}
	if c.Status != "" {
		s.status = c.Status
	}
	if c.StopReason != "" {
		s.stopReason = c.StopReason
	}
	if c.StopDetails != nil {
		s.stopDetails = c.StopDetails
	}
	if c.StopSequence != "" {
		s.stopSequence = c.StopSequence
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

		// Reasoning can follow a message item, as between a commentary
		// message and the final answer.
		if content.Reasoning != nil && !s.SuppressReasoning {
			r := content.Reasoning

			// Native IDs identify reasoning items. Anthropic and Bedrock omit
			// IDs, but a signature ends the previous block; the next delta must
			// start a new item so each block retains its own signed state.
			newID := r.ID != "" && s.reasoningID != "" && r.ID != s.reasoningID
			if s.hasReasoningItem && (newID || r.Redacted || s.reasoningRedacted || (r.ID == "" && s.reasoningSignature != "")) {
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

			s.reasoningRedacted = r.Redacted
			if r.Signature != "" {
				s.reasoningSignature = r.Signature

				if err := s.ensureReasoningItem(); err != nil {
					return err
				}
			}

			for _, part := range r.Content {
				s.reasoningContent.Add(part)
			}
			for _, part := range r.Summary {
				s.reasoningSummary.Add(part)
			}
			if len(r.Content) > 0 || len(r.Summary) > 0 {
				if err := s.ensureReasoningItem(); err != nil {
					return err
				}
			}
			if err := s.emitReasoningParts(false); err != nil {
				return err
			}
		}

		if content.Refusal != "" {
			if err := s.addMessagePart(partRefusal, content.Refusal); err != nil {
				return err
			}
		}

		if content.Text != "" || content.TextPresent {
			if err := s.addMessagePart(partText, content.Text); err != nil {
				return err
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

			currentID, outputIndex, ok := s.trackToolCall(*tc)
			if !ok {
				continue
			}

			if err := s.ensureToolCallStarted(currentID, *tc, outputIndex); err != nil {
				return err
			}

			// Accumulate name and arguments
			entry := &s.toolCalls[s.toolCallByID[currentID]]
			if entry.Closed {
				if tc.Arguments != "" {
					return fmt.Errorf("tool call %s received arguments after completion", currentID)
				}
				continue
			}

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
					ToolCallKind:      entry.Kind,
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
	if s.status == provider.CompletionStatusFailed {
		return s.Error(completionFailure(s.Result()))
	}

	if err := s.start(); err != nil {
		return err
	}

	if err := s.closeCompaction(); err != nil {
		return err
	}

	if err := s.closeReasoning(); err != nil {
		return err
	}
	// Caller-owned calls become executable only after an accepted turn.
	// Hosted calls may already have closed when their result arrived.
	for _, call := range s.toolCalls {
		if err := s.closeToolCall(call.ID); err != nil {
			return err
		}
	}

	if err := s.flushMessage(); err != nil {
		return err
	}

	result := s.Result()
	terminalType := StreamEventResponseCompleted
	if s.itemIncomplete() {
		terminalType = StreamEventResponseIncomplete
	}

	return s.emitEvent(StreamEvent{
		Type:       terminalType,
		Completion: result,
	})
}

// contentFiltered reports a refusal that cannot be represented as a completed
// refusal message, including any turn containing caller-owned tool calls. Native stop
// reasons such as Anthropic's refusal or a Bedrock guardrail halt the output
// after text was streamed, without a refusal part. OpenAI expresses that as
// an incomplete response with the content_filter reason, which clients
// already treat as a turn that must not continue.
func contentFiltered(c *provider.Completion) bool {
	if c == nil || c.Status != provider.CompletionStatusRefused {
		return false
	}
	if c.Message == nil {
		return true
	}
	for _, call := range c.Message.ToolCalls() {
		if callerOwned(call.Kind, call.Execution) {
			return true
		}
	}
	return c.Message.Refusal() == ""
}

func (s *StreamingAccumulator) contentFiltered() bool {
	if s.status != provider.CompletionStatusRefused {
		return false
	}
	for _, call := range s.toolCalls {
		if callerOwned(call.Kind, call.Execution) {
			return true
		}
	}

	for _, message := range s.messages {
		if message.streamedRefusal.Len() > 0 {
			return false
		}
	}
	return true
}

// callerOwned reports whether the client executes the call. Hosted tool search
// runs server side and has already finished when its result arrives.
func callerOwned(kind provider.ToolKind, execution string) bool {
	return kind != provider.ToolKindToolSearch || execution == "client"
}

func (s *StreamingAccumulator) itemIncomplete() bool {
	return s.status == provider.CompletionStatusIncomplete || s.status == provider.CompletionStatusFailed || s.contentFiltered()
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

	if s.message.hasContentPart || s.message.streamedText.Len() > 0 || s.message.streamedRefusal.Len() > 0 {
		return s.nextMessage(id, phase)
	}

	s.message.id = id
	s.message.phase = phase

	return nil
}

// detachClosedMessage starts a new item for text arriving after the current
// message was closed by a tool call. The closed item already streamed its done
// event, so growing it would make the final snapshot disagree with the stream.
func (s *StreamingAccumulator) detachClosedMessage() {
	if !s.message.messageClosed {
		return
	}

	s.message = &streamMessage{}
	s.messages = append(s.messages, s.message)
}

type messagePart int

const (
	partText messagePart = iota
	partRefusal
)

// addMessagePart records a streamed text or refusal delta. Without tool calls,
// or with a native message ID, it streams in place; ID-less text after tool
// calls is withheld and flushed at completion so items stay sequential.
func (s *StreamingAccumulator) addMessagePart(part messagePart, delta string) error {
	if err := s.closeCompaction(); err != nil {
		return err
	}

	s.detachClosedMessage()

	if part == partRefusal {
		if s.message.streamedRefusal.Len() == 0 && s.message.streamedText.Len() == 0 {
			s.message.refusalFirst = true
		}
		s.message.streamedRefusal.WriteString(delta)
	} else {
		s.message.streamedText.WriteString(delta)
	}

	if s.hasOpenToolCall() && s.message.id == "" {
		return nil
	}

	if err := s.closeReasoning(); err != nil {
		return err
	}

	return s.emitMessagePart(part, delta, true)
}

// emitMessagePart opens the message item and the part, then streams the delta.
// present opens an explicit empty text part without a delta.
func (s *StreamingAccumulator) emitMessagePart(part messagePart, delta string, present bool) error {
	if delta == "" && !present {
		return nil
	}

	if err := s.ensureMessageItem(); err != nil {
		return err
	}

	event := StreamEvent{Delta: delta, OutputIndex: s.message.messageOutputIndex}

	if part == partRefusal {
		if err := s.ensureMessageRefusalPart(); err != nil {
			return err
		}
		event.Type, event.ContentIndex = StreamEventRefusalDelta, s.refusalContentIndex()
	} else {
		if err := s.ensureMessageContentPart(); err != nil {
			return err
		}
		event.Type, event.ContentIndex = StreamEventTextDelta, s.textContentIndex()
	}

	if delta == "" {
		return nil
	}

	return s.emitEvent(event)
}

// nextMessage closes the current message item and starts a new one.
func (s *StreamingAccumulator) nextMessage(id string, phase provider.MessagePhase) error {
	if err := s.flushMessage(); err != nil {
		return err
	}

	s.message = &streamMessage{id: id, phase: phase}
	s.messages = append(s.messages, s.message)

	return nil
}

func (s *StreamingAccumulator) flushMessage() error {
	if err := s.closeReasoning(); err != nil {
		return err
	}

	if !s.message.hasOutputItem {
		text, refusal := s.message.streamedText.String(), s.message.streamedRefusal.String()

		// Tool-call parsers leak the newline after a call; it is not a message.
		if refusal == "" && strings.TrimSpace(text) == "" {
			s.message.streamedText.Reset()
			return nil
		}

		// The snapshot splits items by ID; without one, withheld text would
		// fold into the message that preceded the tool calls.
		if s.message.id == "" {
			s.message.id = "msg_" + uuid.NewString()
		}

		parts := []messagePart{partText, partRefusal}
		if s.message.refusalFirst {
			parts[0], parts[1] = parts[1], parts[0]
		}
		for _, part := range parts {
			delta := text
			if part == partRefusal {
				delta = refusal
			}
			if err := s.emitMessagePart(part, delta, false); err != nil {
				return err
			}
		}
	}

	return s.closeMessage()
}

func (s *StreamingAccumulator) Error(err error) error {
	_ = s.start()
	s.status = provider.CompletionStatusFailed
	// Never finalize open calls after a failed turn. Callers may execute them
	// immediately on output_item.done, before seeing response.failed.
	s.pendingEvents = nil

	if e := s.emitEvent(StreamEvent{
		Type:  StreamEventResponseError,
		Error: err,
	}); e != nil {
		return e
	}

	return s.emitEvent(StreamEvent{
		Type:       StreamEventResponseFailed,
		Error:      err,
		Completion: s.Result(),
	})
}

// Result builds the accumulated completion from the accumulator's own state.
func (s *StreamingAccumulator) Result() *provider.Completion {
	var content []provider.Content

	appendMessage := func(m *streamMessage) {
		start := len(content)
		if m.hasContentPart || m.streamedText.Len() > 0 {
			content = append(content, provider.Content{MessageID: m.id, Phase: m.phase, Text: m.streamedText.String(), TextPresent: m.streamedText.Len() == 0})
		}
		if m.streamedRefusal.Len() > 0 {
			content = append(content, provider.Content{MessageID: m.id, Phase: m.phase, Refusal: m.streamedRefusal.String()})
		}
		if m.refusalFirst && len(content)-start == 2 {
			content[start], content[start+1] = content[start+1], content[start]
		}
	}

	for i, ref := range s.outputOrder {
		if s.status == provider.CompletionStatusFailed && s.outputStatuses[i] == "" {
			break // This item was queued and never announced to the client.
		}
		switch ref.kind {
		case streamItemMessage:
			appendMessage(s.messages[ref.index])

		case streamItemReasoning:
			if ref.index < len(s.completedReasonings) {
				content = append(content, provider.ReasoningContent(s.completedReasonings[ref.index]))
			} else {
				content = append(content, provider.ReasoningContent(s.currentReasoning()))
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
			if !s.itemIncomplete() {
				call = provider.NormalizeToolCallArguments(call)
			}

			content = append(content, provider.ToolCallContent(call))
		case streamItemToolResult:
			content = append(content, provider.ToolResultContent(s.toolResults[ref.index]))
		}
	}

	// Before Complete, trailing text may still be waiting for tool calls to
	// finish. Include it in partial snapshots without assigning an index early.
	if !s.message.hasOutputItem && s.status != provider.CompletionStatusFailed {
		appendMessage(s.message)
	}

	return &provider.Completion{
		ID:           s.id,
		Model:        s.model,
		Status:       s.status,
		StopReason:   s.stopReason,
		StopDetails:  s.stopDetails,
		StopSequence: s.stopSequence,
		Usage:        s.usage,
		Reasoning:    s.reasoningContext,
		Tier:         s.tier,

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Phase:   s.message.phase,
			Content: content,
		},
	}
}

func (s *StreamingAccumulator) emitEvent(event StreamEvent) error {
	switch event.Type {
	case StreamEventResponseCompleted, StreamEventResponseIncomplete, StreamEventResponseFailed:
		event.OutputStatuses = append([]string(nil), s.outputStatuses...)
		for i, status := range event.OutputStatuses {
			if status == "" || status == "in_progress" {
				event.OutputStatuses[i] = itemStatus(event.Type != StreamEventResponseCompleted)
			}
		}
	}
	event.MessageIndex = len(s.messages) - 1
	event.MessageID = s.message.id
	if strings.HasPrefix(string(event.Type), "response.") {
		if s.handler != nil {
			return s.handler(event)
		}
		return nil
	}
	if event.OutputIndex == s.nextEventIndex && len(s.pendingEvents[s.nextEventIndex]) == 0 {
		if err := s.deliverEvent(event); err != nil {
			return err
		}
	} else {
		if s.pendingEvents == nil {
			s.pendingEvents = make(map[int][]StreamEvent)
		}
		s.pendingEvents[event.OutputIndex] = append(s.pendingEvents[event.OutputIndex], event)
	}

	for len(s.pendingEvents[s.nextEventIndex]) > 0 {
		events := s.pendingEvents[s.nextEventIndex]
		next := events[0]
		if len(events) == 1 {
			delete(s.pendingEvents, s.nextEventIndex)
		} else {
			s.pendingEvents[s.nextEventIndex] = events[1:]
		}
		if err := s.deliverEvent(next); err != nil {
			return err
		}

	}
	return nil
}

func (s *StreamingAccumulator) deliverEvent(event StreamEvent) error {
	if s.handler != nil {
		if err := s.handler(event); err != nil {
			return err
		}
	}
	switch event.Type {
	case StreamEventOutputItemAdded, StreamEventReasoningItemAdded, StreamEventCompactionItemAdded, StreamEventFunctionCallAdded:
		s.outputStatuses[event.OutputIndex] = "in_progress"
	case StreamEventOutputItemDone, StreamEventReasoningItemDone, StreamEventCompactionItemDone, StreamEventFunctionCallDone, StreamEventToolSearchResult:
		s.outputStatuses[event.OutputIndex] = itemStatus(event.Incomplete)
		s.nextEventIndex++
	}
	return nil
}

func (s *StreamingAccumulator) hasOpenToolCall() bool {
	for _, call := range s.toolCalls {
		if !call.Closed {
			return true
		}
	}
	return false
}
