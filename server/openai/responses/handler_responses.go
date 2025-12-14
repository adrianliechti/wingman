package responses

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"
)

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	var req ResponsesRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.Input.Items, req.Instructions)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools, err := toTools(req.Tools)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.CompleteOptions{
		//Stop:  stops,
		Tools: tools,

		//MaxTokens:   req.MaxTokens,
		//Temperature: req.Temperature,
	}

	// Handle structured output configuration
	if req.Text != nil && req.Text.Format != nil {
		if req.Text.Format.Type == "json_object" || req.Text.Format.Type == "json_schema" {
			options.Format = provider.CompletionFormatJSON
		}

		if req.Text.Format.Type == "json_schema" && req.Text.Format.Schema != nil {
			options.Schema = &provider.Schema{
				Name:        req.Text.Format.Name,
				Description: req.Text.Format.Description,
				Strict:      req.Text.Format.Strict,
				Schema:      req.Text.Format.Schema,
			}
		}
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		responseID := "resp_" + uuid.NewString()
		messageID := "msg_" + uuid.NewString()
		createdAt := time.Now().Unix()
		seqNum := 0

		// Helper to get sequence number and increment
		nextSeq := func() int {
			n := seqNum
			seqNum++
			return n
		}

		// Create initial response template
		createResponse := func(status string, output []ResponseOutput) *Response {
			return &Response{
				ID:        responseID,
				Object:    "response",
				CreatedAt: createdAt,
				Status:    status,
				Model:     req.Model,
				Output:    output,
			}
		}

		// Create streaming accumulator with event handler
		accumulator := NewStreamingAccumulator(func(event StreamEvent) error {
			switch event.Type {
			case StreamEventResponseCreated:
				return writeEvent(w, "response.created", ResponseCreatedEvent{
					Type:           "response.created",
					SequenceNumber: nextSeq(),
					Response:       createResponse("in_progress", []ResponseOutput{}),
				})

			case StreamEventResponseInProgress:
				return writeEvent(w, "response.in_progress", ResponseInProgressEvent{
					Type:           "response.in_progress",
					SequenceNumber: nextSeq(),
					Response:       createResponse("in_progress", []ResponseOutput{}),
				})

			case StreamEventOutputItemAdded:
				return writeEvent(w, "response.output_item.added", OutputItemAddedEvent{
					Type:           "response.output_item.added",
					SequenceNumber: nextSeq(),
					OutputIndex:    0,
					Item: &OutputItem{
						ID:      messageID,
						Type:    "message",
						Status:  "in_progress",
						Content: []OutputContent{},
						Role:    MessageRoleAssistant,
					},
				})

			case StreamEventContentPartAdded:
				return writeEvent(w, "response.content_part.added", ContentPartAddedEvent{
					Type:           "response.content_part.added",
					SequenceNumber: nextSeq(),
					ItemID:         messageID,
					OutputIndex:    0,
					ContentIndex:   0,
					Part: &OutputContent{
						Type: "output_text",
						Text: "",
					},
				})

			case StreamEventTextDelta:
				return writeEvent(w, "response.output_text.delta", OutputTextDeltaEvent{
					Type:           "response.output_text.delta",
					SequenceNumber: nextSeq(),
					ItemID:         messageID,
					OutputIndex:    0,
					ContentIndex:   0,
					Delta:          event.Delta,
				})

			case StreamEventTextDone:
				return writeEvent(w, "response.output_text.done", OutputTextDoneEvent{
					Type:           "response.output_text.done",
					SequenceNumber: nextSeq(),
					ItemID:         messageID,
					OutputIndex:    0,
					ContentIndex:   0,
					Text:           event.Text,
				})

			case StreamEventContentPartDone:
				return writeEvent(w, "response.content_part.done", ContentPartDoneEvent{
					Type:           "response.content_part.done",
					SequenceNumber: nextSeq(),
					ItemID:         messageID,
					OutputIndex:    0,
					ContentIndex:   0,
					Part: &OutputContent{
						Type: "output_text",
						Text: event.Text,
					},
				})

			case StreamEventFunctionCallAdded:
				return writeEvent(w, "response.output_item.added", FunctionCallOutputItemAddedEvent{
					Type:           "response.output_item.added",
					SequenceNumber: nextSeq(),
					OutputIndex:    event.OutputIndex,
					Item: &FunctionCallOutputItem{
						ID:        event.ToolCallID,
						Type:      "function_call",
						Status:    "in_progress",
						CallID:    event.ToolCallID,
						Name:      event.ToolCallName,
						Arguments: "",
					},
				})

			case StreamEventFunctionCallArgumentsDelta:
				return writeEvent(w, "response.function_call_arguments.delta", FunctionCallArgumentsDeltaEvent{
					Type:           "response.function_call_arguments.delta",
					SequenceNumber: nextSeq(),
					ItemID:         event.ToolCallID,
					OutputIndex:    event.OutputIndex,
					Delta:          event.Delta,
				})

			case StreamEventFunctionCallArgumentsDone:
				return writeEvent(w, "response.function_call_arguments.done", FunctionCallArgumentsDoneEvent{
					Type:           "response.function_call_arguments.done",
					SequenceNumber: nextSeq(),
					ItemID:         event.ToolCallID,
					Name:           event.ToolCallName,
					OutputIndex:    event.OutputIndex,
					Arguments:      event.Arguments,
				})

			case StreamEventFunctionCallDone:
				return writeEvent(w, "response.output_item.done", FunctionCallOutputItemDoneEvent{
					Type:           "response.output_item.done",
					SequenceNumber: nextSeq(),
					OutputIndex:    event.OutputIndex,
					Item: &FunctionCallOutputItem{
						ID:        event.ToolCallID,
						Type:      "function_call",
						Status:    "completed",
						CallID:    event.ToolCallID,
						Name:      event.ToolCallName,
						Arguments: event.Arguments,
					},
				})

			case StreamEventOutputItemDone:
				return writeEvent(w, "response.output_item.done", OutputItemDoneEvent{
					Type:           "response.output_item.done",
					SequenceNumber: nextSeq(),
					OutputIndex:    0,
					Item: &OutputItem{
						ID:     messageID,
						Type:   "message",
						Status: "completed",
						Content: []OutputContent{
							{
								Type: "output_text",
								Text: event.Completion.Message.Text(),
							},
						},
						Role: MessageRoleAssistant,
					},
				})

			case StreamEventResponseCompleted:
				model := req.Model
				if event.Completion != nil && event.Completion.Model != "" {
					model = event.Completion.Model
				}

				// Build output array based on what was actually in the completion
				// Initialize to empty slice (not nil) to ensure JSON encodes as [] not null
				output := []ResponseOutput{}

				if event.Completion != nil && event.Completion.Message != nil {
					// Add function call outputs first (they appear before messages)
					for _, call := range event.Completion.Message.ToolCalls() {
						output = append(output, ResponseOutput{
							Type: ResponseOutputTypeFunctionCall,
							FunctionCallOutputItem: &FunctionCallOutputItem{
								ID:        call.ID,
								Type:      "function_call",
								Status:    "completed",
								Name:      call.Name,
								CallID:    call.ID,
								Arguments: call.Arguments,
							},
						})
					}

					// Add message output if there's text content
					text := event.Completion.Message.Text()
					if text != "" {
						output = append(output, ResponseOutput{
							Type: ResponseOutputTypeMessage,
							OutputMessage: &OutputMessage{
								ID:     messageID,
								Role:   MessageRoleAssistant,
								Status: "completed",
								Contents: []OutputContent{
									{
										Type: "output_text",
										Text: text,
									},
								},
							},
						})
					}
				}

				return writeEvent(w, "response.completed", ResponseCompletedEvent{
					Type:           "response.completed",
					SequenceNumber: nextSeq(),
					Response: &Response{
						ID:        responseID,
						Object:    "response",
						CreatedAt: createdAt,
						Status:    "completed",
						Model:     model,
						Output:    output,
					},
				})
			}

			return nil
		})

		// Set up stream handler to feed into accumulator
		options.Stream = func(ctx context.Context, completion provider.Completion) error {
			return accumulator.Add(completion)
		}

		_, err = completer.Complete(r.Context(), messages, options)

		if err != nil {
			writeEvent(w, "response.failed", ResponseFailedEvent{
				Type:           "response.failed",
				SequenceNumber: nextSeq(),
				Response: &Response{
					ID:        responseID,
					Object:    "response",
					CreatedAt: createdAt,
					Status:    "failed",
					Model:     req.Model,
					Output:    []ResponseOutput{},
					Error: &ResponseError{
						Code:    "server_error",
						Message: err.Error(),
					},
				},
			})
			return
		}

		// Emit final events
		if err := accumulator.Complete(); err != nil {
			writeEvent(w, "response.failed", ResponseFailedEvent{
				Type:           "response.failed",
				SequenceNumber: nextSeq(),
				Response: &Response{
					ID:        responseID,
					Object:    "response",
					CreatedAt: createdAt,
					Status:    "failed",
					Model:     req.Model,
					Output:    []ResponseOutput{},
					Error: &ResponseError{
						Code:    "server_error",
						Message: err.Error(),
					},
				},
			})
			return
		}
	} else {
		completion, err := completer.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		result := Response{
			Object: "response",
			Status: "completed",

			ID: completion.ID,

			Model:     completion.Model,
			CreatedAt: time.Now().Unix(),

			Output: []ResponseOutput{},
		}

		if result.Model == "" {
			result.Model = req.Model
		}

		if completion.Message != nil {
			output := ResponseOutput{
				Type: ResponseOutputTypeMessage,

				OutputMessage: &OutputMessage{
					Role: "assistant",

					Status: "completed",

					Contents: []OutputContent{
						{
							Type: "output_text",
							Text: completion.Message.Text(),
						},
					},
				},
			}

			result.Output = append(result.Output, output)
		}

		writeJson(w, result)
	}
}

func toMessages(items []InputItem, instructions string) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	// Add instructions as system message if provided
	if instructions != "" {
		result = append(result, provider.Message{
			Role:    provider.MessageRoleSystem,
			Content: []provider.Content{provider.TextContent(instructions)},
		})
	}

	// Track pending tool calls to merge with their results
	var pendingToolCalls []provider.ToolCall

	for _, item := range items {
		switch item.Type {
		case InputItemTypeMessage:
			if item.InputMessage == nil {
				continue
			}
			m := item.InputMessage
			var content []provider.Content

			for _, c := range m.Content {
				if c.Type == InputContentText {
					content = append(content, provider.TextContent(c.Text))
				}

				if c.Type == InputContentImage && c.ImageURL != "" {
					file, err := toFile(c.ImageURL)
					if err != nil {
						return nil, err
					}
					content = append(content, provider.FileContent(file))
				}
			}

			// Add any pending tool calls to the assistant message
			if m.Role == MessageRoleAssistant && len(pendingToolCalls) > 0 {
				for _, tc := range pendingToolCalls {
					content = append(content, provider.ToolCallContent(tc))
				}
				pendingToolCalls = nil
			}

			if len(content) > 0 {
				result = append(result, provider.Message{
					Role:    toMessageRole(m.Role),
					Content: content,
				})
			}

		case InputItemTypeReasoning:
			// Skip reasoning items - they are internal to the model
			continue

		case InputItemTypeFunctionCall:
			if item.InputFunctionCall == nil {
				continue
			}
			fc := item.InputFunctionCall

			// Collect function calls to be added to the next assistant message
			// or create a standalone assistant message with the tool call
			toolCall := provider.ToolCall{
				ID:        fc.CallID,
				Name:      fc.Name,
				Arguments: fc.Arguments,
			}

			// Add as assistant message with tool call
			result = append(result, provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(toolCall),
				},
			})

		case InputItemTypeFunctionCallOutput:
			if item.InputFunctionCallOutput == nil {
				continue
			}
			fco := item.InputFunctionCallOutput

			// Add as user message with tool result
			result = append(result, provider.Message{
				Role: provider.MessageRoleUser,
				Content: []provider.Content{
					provider.ToolResultContent(provider.ToolResult{
						ID:   fco.CallID,
						Data: fco.Output,
					}),
				},
			})
		}
	}

	return result, nil
}

func toTools(tools []Tool) ([]provider.Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	result := make([]provider.Tool, 0, len(tools))

	for _, t := range tools {
		// Only support function tools for now
		// Custom tools (like apply_patch) require special handling by the model
		if t.Type == ToolTypeFunction {
			tool := provider.Tool{
				Name:        t.Name,
				Description: t.Description,
				Strict:      t.Strict,
				Parameters:  t.Parameters,
			}
			result = append(result, tool)
		}
		// Note: Custom tools with grammar format are passed through to the model
		// but may require special handling in the completer
	}

	return result, nil
}

func toMessageRole(r MessageRole) provider.MessageRole {
	switch r {
	case MessageRoleSystem:
		return provider.MessageRoleSystem

	case MessageRoleUser: // MessageRoleTool
		return provider.MessageRoleUser

	case MessageRoleAssistant:
		return provider.MessageRoleAssistant

	default:
		return ""
	}
}

func toFile(url string) (*provider.File, error) {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		resp, err := http.Get(url)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)

		if err != nil {
			return nil, err
		}

		file := provider.File{
			Content:     data,
			ContentType: resp.Header.Get("Content-Type"),
		}

		if ext, _ := mime.ExtensionsByType(file.ContentType); len(ext) > 0 {
			file.Name = uuid.New().String() + ext[0]
		}

		return &file, nil
	}

	if strings.HasPrefix(url, "data:") {
		re := regexp.MustCompile(`data:([a-zA-Z]+\/[a-zA-Z0-9.+_-]+);base64,\s*(.+)`)

		match := re.FindStringSubmatch(url)

		if len(match) != 3 {
			return nil, fmt.Errorf("invalid data url")
		}

		data, err := base64.StdEncoding.DecodeString(match[2])

		if err != nil {
			return nil, fmt.Errorf("invalid data encoding")
		}

		file := provider.File{
			Content:     data,
			ContentType: match[1],
		}

		if ext, _ := mime.ExtensionsByType(file.ContentType); len(ext) > 0 {
			file.Name = uuid.New().String() + ext[0]
		}

		return &file, nil
	}

	return nil, fmt.Errorf("invalid url")
}
