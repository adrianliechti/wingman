package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	var req MessageRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Parse system content
	system, err := parseSystemContent(req.System)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Convert messages
	messages, err := toMessages(system, req.Messages)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Convert tools
	tools := toTools(req.Tools)

	// Build options
	options := &provider.CompleteOptions{
		Stop:        req.StopSequences,
		Tools:       tools,
		Temperature: req.Temperature,
	}

	if req.MaxTokens > 0 {
		options.MaxTokens = &req.MaxTokens
	}

	if req.Stream {
		h.handleMessagesStream(w, r, req, completer, messages, options)
	} else {
		h.handleMessagesComplete(w, r, req, completer, messages, options)
	}
}

func (h *Handler) handleMessagesComplete(w http.ResponseWriter, r *http.Request, req MessageRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		acc.Add(*completion)
	}

	completion := acc.Result()

	var content []ContentBlock
	if completion.Message != nil {
		content = toContentBlocks(completion.Message.Content)
	}

	if len(content) == 0 {
		content = []ContentBlock{}
	}

	stopReason := StopReasonEndTurn
	if completion.Message != nil {
		stopReason = toStopReason(completion.Message.Content)
	}

	var usage Usage
	if completion.Usage != nil {
		usage = Usage{
			InputTokens:  completion.Usage.InputTokens,
			OutputTokens: completion.Usage.OutputTokens,
		}
	}

	model := completion.Model
	if model == "" {
		model = req.Model
	}

	result := Message{
		ID:         generateMessageID(),
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      model,
		StopReason: stopReason,
		Usage:      usage,
	}

	writeJson(w, result)
}

func (h *Handler) handleMessagesStream(w http.ResponseWriter, r *http.Request, req MessageRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	messageID := generateMessageID()
	model := req.Model

	// Track streaming state
	var (
		outputTokens      int
		currentBlockIndex = -1
		currentBlockType  = ""
		toolCallID        = ""
		toolCallName      = ""
		toolCallArgs      = ""
		hasContent        = false
		stopReason        = StopReasonEndTurn
	)

	acc := provider.CompletionAccumulator{}

	// Send message_start event
	initialMessage := Message{
		ID:      messageID,
		Type:    "message",
		Role:    "assistant",
		Content: []ContentBlock{},
		Model:   model,
		Usage: Usage{
			InputTokens:  0,
			OutputTokens: 0,
		},
	}

	if err := writeEvent(w, "message_start", MessageStartEvent{
		Type:    "message_start",
		Message: initialMessage,
	}); err != nil {
		return
	}

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			// Send error event
			writeEvent(w, "error", ErrorResponse{
				Type: "error",
				Error: Error{
					Type:    "api_error",
					Message: err.Error(),
				},
			})
			return
		}

		acc.Add(*completion)

		if completion.Model != "" {
			model = completion.Model
		}

		// Update usage
		if completion.Usage != nil {
			outputTokens = completion.Usage.OutputTokens
		}

		if completion.Message == nil || len(completion.Message.Content) == 0 {
			continue
		}

		for _, c := range completion.Message.Content {
			// Handle text content
			if c.Text != "" {
				// Start text block if needed
				if currentBlockType != "text" {
					// Close previous block if any
					if currentBlockIndex >= 0 {
						writeEvent(w, "content_block_stop", ContentBlockStopEvent{
							Type:  "content_block_stop",
							Index: currentBlockIndex,
						})
					}

					currentBlockIndex++
					currentBlockType = "text"
					hasContent = true

					writeEvent(w, "content_block_start", ContentBlockStartEvent{
						Type:  "content_block_start",
						Index: currentBlockIndex,
						ContentBlock: ContentBlock{
							Type: "text",
							Text: "",
						},
					})
				}

				// Send text delta
				writeEvent(w, "content_block_delta", ContentBlockDeltaEvent{
					Type:  "content_block_delta",
					Index: currentBlockIndex,
					Delta: Delta{
						Type: "text_delta",
						Text: c.Text,
					},
				})
			}

			// Handle tool calls
			if c.ToolCall != nil {
				stopReason = StopReasonToolUse

				// Check if this is a new tool call or continuation
				isNewToolCall := c.ToolCall.ID != "" && c.ToolCall.ID != toolCallID

				if isNewToolCall {
					// Close previous block if any
					if currentBlockIndex >= 0 {
						writeEvent(w, "content_block_stop", ContentBlockStopEvent{
							Type:  "content_block_stop",
							Index: currentBlockIndex,
						})
					}

					currentBlockIndex++
					currentBlockType = "tool_use"
					toolCallID = c.ToolCall.ID
					if toolCallID == "" {
						toolCallID = generateToolUseID()
					}
					toolCallName = c.ToolCall.Name
					toolCallArgs = ""
					hasContent = true

					// Send content_block_start for tool_use
					writeEvent(w, "content_block_start", ContentBlockStartEvent{
						Type:  "content_block_start",
						Index: currentBlockIndex,
						ContentBlock: ContentBlock{
							Type:  "tool_use",
							ID:    toolCallID,
							Name:  toolCallName,
							Input: map[string]any{},
						},
					})
				}

				// Send input_json_delta if there are arguments
				if c.ToolCall.Arguments != "" {
					toolCallArgs += c.ToolCall.Arguments

					writeEvent(w, "content_block_delta", ContentBlockDeltaEvent{
						Type:  "content_block_delta",
						Index: currentBlockIndex,
						Delta: Delta{
							Type:        "input_json_delta",
							PartialJSON: c.ToolCall.Arguments,
						},
					})
				}
			}
		}
	}

	// Close last content block if any
	if currentBlockIndex >= 0 {
		writeEvent(w, "content_block_stop", ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: currentBlockIndex,
		})
	}

	// If no content was generated, send an empty text block
	if !hasContent {
		writeEvent(w, "content_block_start", ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: 0,
			ContentBlock: ContentBlock{
				Type: "text",
				Text: "",
			},
		})
		writeEvent(w, "content_block_stop", ContentBlockStopEvent{
			Type:  "content_block_stop",
			Index: 0,
		})
	}

	// Determine final stop reason from accumulated result
	finalCompletion := acc.Result()
	if finalCompletion.Message != nil {
		stopReason = toStopReason(finalCompletion.Message.Content)
	}

	// Send message_delta with stop_reason and usage
	writeEvent(w, "message_delta", MessageDeltaEvent{
		Type: "message_delta",
		Delta: MessageDelta{
			StopReason: stopReason,
		},
		Usage: DeltaUsage{
			OutputTokens: outputTokens,
		},
	})

	// Send message_stop
	writeEvent(w, "message_stop", MessageStopEvent{
		Type: "message_stop",
	})
}
