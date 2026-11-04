package google

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genproto/googleapis/rpc/code"
)

type interactionStep struct {
	call         *interactions.FunctionCallStep
	arguments    strings.Builder
	media        map[string]string
	closed       bool
	summaryIndex int
}

type interactionState struct {
	options *provider.CompleteOptions
	aliases map[string]provider.Tool

	id, model string
	tier      provider.Tier

	status     provider.CompletionStatus
	stopReason provider.StopReason
	details    *provider.StopDetails
	terminal   bool
	completed  bool

	steps   map[int]*interactionStep
	sawTool bool
}

func newInteractionState(model string, options *provider.CompleteOptions) *interactionState {
	return &interactionState{
		id: "gemini_" + generateCallID(), model: model, options: options,
		aliases: provider.ToolAliases(options.Tools), steps: map[int]*interactionStep{},
	}
}

func (s *interactionState) setIdentity(id, model string) {
	if id != "" {
		s.id = id
	}
	if model != "" {
		s.model = model
	}
}

func (s *interactionState) completion() *provider.Completion {
	return &provider.Completion{
		ID: s.id, Model: s.model,
		Tier:    s.tier,
		Message: &provider.Message{Role: provider.MessageRoleAssistant},
	}
}

func (s *interactionState) itemID(index int) string {
	return s.id + ":" + strconv.Itoa(index)
}

func (s *interactionState) event(event interactions.InteractionSSEEvent) (*provider.Completion, error) {
	switch {
	case event.InteractionCreatedEvent != nil:
		interaction := event.InteractionCreatedEvent.Interaction
		s.setIdentity(interaction.ID, value(interaction.Model))
		s.setTier(interaction.ServiceTier)
		return s.completion(), nil
	case event.StepStart != nil:
		return s.start(event.StepStart.Index, event.StepStart.Step)
	case event.StepDelta != nil:
		return s.delta(event.StepDelta)
	case event.StepStop != nil:
		delta, err := s.stop(event.StepStop.Index)
		if event.StepStop.Usage != nil {
			if delta == nil {
				delta = s.completion()
			}
			// usage is cumulative; step_usage is only for the individual step.
			delta.Usage = toCompletionUsage(event.StepStop.Usage)
		}
		return delta, err
	case event.InteractionStatusUpdate != nil:
		s.setIdentity(event.InteractionStatusUpdate.InteractionID, "")
		status := string(event.InteractionStatusUpdate.Status)
		if status == "in_progress" || status == "queued" {
			return nil, nil
		}
		if status == "completed" || status == "requires_action" {
			if err := s.checkStoppedTools(); err != nil {
				return nil, err
			}
		}
		return s.finish(status, event.InteractionStatusUpdate.Usage), nil
	case event.InteractionCompletedEvent != nil:
		interaction := event.InteractionCompletedEvent.Interaction
		s.setTier(interaction.ServiceTier)
		status := string(interaction.Status)
		if status == "completed" || status == "requires_action" {
			if err := s.checkStoppedTools(); err != nil {
				return nil, err
			}
		}
		s.setIdentity(interaction.ID, value(interaction.Model))
		// A terminal summary can repeat the whole timeline. The individual
		// steps have already been emitted and must not be emitted twice.
		s.completed = true
		return s.finish(status, interaction.Usage), nil
	case event.ErrorEvent != nil:
		err := event.ErrorEvent.Error
		return nil, interactionProviderError(fmt.Errorf("gemini: %s", value(err.GetMessage())), 0, value(err.GetCode()), value(err.GetMessage()), "", nil)
	case event.IsUnknown():
		return nil, fmt.Errorf("gemini: unsupported interaction event: %s", event.UnknownRaw)
	}
	return nil, nil
}

func (s *interactionState) checkStoppedTools() error {
	for index, step := range s.steps {
		if step.call != nil && !step.closed {
			return fmt.Errorf("gemini: interaction ended before tool step %d stopped", index)
		}
	}
	return nil
}

func (s *interactionState) start(index int, step interactions.Step) (*provider.Completion, error) {
	if _, exists := s.steps[index]; exists {
		return nil, fmt.Errorf("gemini: duplicate step %d", index)
	}
	state := &interactionStep{media: map[string]string{}}
	s.steps[index] = state
	delta := s.completion()
	switch {
	case step.FunctionCallStep != nil:
		call := *step.FunctionCallStep
		if call.ID == "" {
			call.ID = generateCallID()
		}
		state.call = &call
		s.sawTool = true
		// Function arguments arrive as strings in arguments_delta events.
		// Emit the identity now to preserve timeline order; each call's complete
		// arguments (including custom-tool unwrapping) follow at step.stop.
		delta.Message.Content = []provider.Content{provider.ToolCallContent(s.toolCall(call.ID, call.Name, ""))}
	case step.ThoughtStep != nil:
		thought := step.ThoughtStep
		var summary []provider.Part
		for _, content := range thought.Summary {
			if content.TextContent != nil {
				summary = append(summary, provider.Part{Index: len(summary), Text: content.TextContent.Text})
			}
		}
		state.summaryIndex = max(0, len(summary)-1)
		delta.Message.Content = []provider.Content{provider.ReasoningContent(provider.Reasoning{
			ID: s.itemID(index), Summary: summary, Signature: value(thought.Signature),
		})}
	case step.ModelOutputStep != nil:
		for _, content := range step.ModelOutputStep.Content {
			part, kind, err := s.content(content)
			if err != nil {
				return nil, err
			}
			part.MessageID = s.itemID(index)
			// A start frame can declare media metadata before any bytes.
			switch kind {
			case "image":
				state.media[kind] = string(value(content.ImageContent.MimeType))
			case "audio":
				state.media[kind] = string(value(content.AudioContent.MimeType))
			case "document":
				state.media[kind] = string(value(content.DocumentContent.MimeType))
			case "video":
				state.media[kind] = string(value(content.VideoContent.MimeType))
			}
			if part.Text != "" || part.File != nil {
				delta.Message.Content = append(delta.Message.Content, part)
			}
		}
		if err := step.ModelOutputStep.Error; err != nil && value(err.Code) != 0 {
			if failure := s.modelError(err); failure != nil {
				return nil, failure
			}
			delta.Status, delta.StopReason, delta.StopDetails = s.status, s.stopReason, s.details
		}
	default:
		return nil, fmt.Errorf("gemini: unsupported output step %q", step.Type)
	}
	if len(delta.Message.Content) == 0 && delta.Status == "" {
		return nil, nil
	}
	return delta, nil
}

func (s *interactionState) delta(event *interactions.StepDelta) (*provider.Completion, error) {
	step := s.steps[event.Index]
	if step == nil || step.closed {
		return nil, fmt.Errorf("gemini: delta for inactive step %d", event.Index)
	}
	delta := s.completion()
	data := event.Delta
	var part provider.Content
	switch {
	case data.ArgumentsDelta != nil:
		if step.call == nil {
			return nil, fmt.Errorf("gemini: arguments for non-function step %d", event.Index)
		}
		step.arguments.WriteString(value(data.ArgumentsDelta.Arguments))
	case data.TextDelta != nil:
		part.Text = data.TextDelta.Text
		part.MessageID = s.itemID(event.Index)
	case data.ThoughtSummaryDelta != nil:
		content := data.ThoughtSummaryDelta.Content
		if content != nil && content.TextContent != nil {
			part.Reasoning = &provider.Reasoning{ID: s.itemID(event.Index), Summary: []provider.Part{{Index: step.summaryIndex, Text: content.TextContent.Text}}}
		}
	case data.ThoughtSignatureDelta != nil:
		part.Reasoning = &provider.Reasoning{ID: s.itemID(event.Index), Signature: value(data.ThoughtSignatureDelta.Signature)}
	case data.ImageDelta != nil:
		media := data.ImageDelta
		file, err := streamedFile(media.Data, media.URI, string(value(media.MimeType)), "image", step)
		if err != nil {
			return nil, err
		}
		part.File = file
	case data.AudioDelta != nil:
		media := data.AudioDelta
		file, err := streamedFile(media.Data, media.URI, string(value(media.MimeType)), "audio", step)
		if err != nil {
			return nil, err
		}
		part.File = file
	case data.DocumentDelta != nil:
		media := data.DocumentDelta
		file, err := streamedFile(media.Data, media.URI, string(value(media.MimeType)), "document", step)
		if err != nil {
			return nil, err
		}
		part.File = file
	case data.VideoDelta != nil:
		media := data.VideoDelta
		file, err := streamedFile(media.Data, media.URI, string(value(media.MimeType)), "video", step)
		if err != nil {
			return nil, err
		}
		part.File = file
	case data.TextAnnotationDelta != nil:
		// Wingman's completion interface does not expose citations.
	case data.IsUnknown():
		return nil, fmt.Errorf("gemini: unsupported step delta: %s", data.UnknownRaw)
	default:
		return nil, fmt.Errorf("gemini: unsupported step delta %q", data.Type)
	}
	if part.Text != "" || part.Reasoning != nil || part.File != nil {
		delta.Message.Content = append(delta.Message.Content, part)
	}
	if event.Metadata != nil {
		delta.Usage = toCompletionUsage(event.Metadata.TotalUsage)
	}
	if len(delta.Message.Content) == 0 && delta.Usage == nil {
		return nil, nil
	}
	return delta, nil
}

func (s *interactionState) stop(index int) (*provider.Completion, error) {
	step := s.steps[index]
	if step == nil || step.closed {
		return nil, fmt.Errorf("gemini: stop for inactive step %d", index)
	}
	step.closed = true
	if step.call == nil {
		return nil, nil
	}
	arguments := step.arguments.String()
	if arguments == "" {
		arguments = "{}"
		if step.call.Arguments != nil {
			data, err := json.Marshal(step.call.Arguments)
			if err != nil {
				return nil, err
			}
			arguments = string(data)
		}
	}
	if _, err := toolArguments(arguments); err != nil {
		return nil, fmt.Errorf("gemini: invalid arguments for tool call %q", step.call.ID)
	}
	call := s.toolCall(step.call.ID, step.call.Name, arguments)
	if custom.IsEmulated(s.options.Tools, step.call.Name) {
		call.Arguments = custom.Unwrap(arguments)
	}
	delta := s.completion()
	delta.Message.Content = []provider.Content{provider.ToolCallContent(call)}
	return delta, nil
}

func (s *interactionState) toolCall(id, name, arguments string) provider.ToolCall {
	call := provider.UnflattenToolCall(s.aliases, provider.ToolCall{ID: id, Name: name, Arguments: arguments})
	if custom.IsEmulated(s.options.Tools, name) {
		call.Kind = provider.ToolKindCustom
	}
	return call
}

func (s *interactionState) finish(status string, usage *interactions.Usage) *provider.Completion {
	delta := s.completion()
	delta.Usage = toCompletionUsage(usage)
	switch status {
	case "completed", "requires_action":
		delta.Status = provider.CompletionStatusCompleted
		delta.StopReason = provider.StopReasonEndTurn
		if s.sawTool || status == "requires_action" {
			delta.StopReason = provider.StopReasonToolUse
		}
	case "incomplete", "budget_exceeded":
		delta.Status = provider.CompletionStatusIncomplete
		delta.StopReason = provider.StopReasonMaxTokens
	case "failed", "cancelled":
		delta.Status = provider.CompletionStatusFailed
		delta.StopDetails = &provider.StopDetails{Type: status}
	default:
		delta.Status = provider.CompletionStatusFailed
		delta.StopDetails = &provider.StopDetails{Type: "unexpected_status", Explanation: status}
	}
	if s.status != "" {
		delta.Status, delta.StopReason, delta.StopDetails = s.status, s.stopReason, s.details
	}
	s.terminal = true
	return delta
}

func (s *interactionState) modelError(err *interactions.Status) error {
	s.status = provider.CompletionStatusFailed
	s.details = &provider.StopDetails{Type: strconv.Itoa(value(err.Code)), Explanation: value(err.Message)}
	for _, detail := range err.Details {
		if reason, ok := detail["reason"].(string); ok {
			s.details.Category = reason
		}
	}
	message := strings.ToLower(s.details.Explanation)
	category := strings.ToUpper(s.details.Category)
	if strings.Contains(message, "safety") || category == "SAFETY" || category == "PROHIBITED_CONTENT" || category == "RECITATION" || category == "BLOCKLIST" || category == "SPII" {
		s.status, s.stopReason = provider.CompletionStatusRefused, provider.StopReasonRefusal
		s.details.Type = "refusal"
	} else if value(err.Code) == 11 {
		s.status, s.stopReason = provider.CompletionStatusIncomplete, provider.StopReasonMaxTokens
	} else {
		// Model output errors use google.rpc.Code numbers, not HTTP codes.
		errType := code.Code(value(err.Code)).String()
		message := value(err.Message)
		if message == "" {
			message = "gemini: model request failed"
		}
		return &provider.ProviderError{Code: statusCodeFromErrorType(errType), Type: errType, Message: message, RetryAfter: retryFromDetails(err.Details)}
	}
	return nil
}

func toCompletionUsage(usage *interactions.Usage) *provider.Usage {
	if usage == nil {
		return nil
	}
	// The API reports visible output and thoughts separately. Keep the
	// provider's output count inclusive of reasoning, including measured zero.
	return &provider.Usage{
		InputTokens:          value(usage.TotalInputTokens),
		OutputTokens:         value(usage.TotalOutputTokens) + value(usage.TotalThoughtTokens),
		ReasoningTokens:      usage.TotalThoughtTokens,
		CacheReadInputTokens: value(usage.TotalCachedTokens),
	}
}

func (s *interactionState) content(content interactions.Content) (provider.Content, string, error) {
	switch {
	case content.TextContent != nil:
		return provider.TextContent(content.TextContent.Text), "text", nil
	case content.ImageContent != nil:
		m := content.ImageContent
		return mediaContent(m.Data, m.URI, string(value(m.MimeType)), "image")
	case content.AudioContent != nil:
		m := content.AudioContent
		return mediaContent(m.Data, m.URI, string(value(m.MimeType)), "audio")
	case content.DocumentContent != nil:
		m := content.DocumentContent
		return mediaContent(m.Data, m.URI, string(value(m.MimeType)), "document")
	case content.VideoContent != nil:
		m := content.VideoContent
		return mediaContent(m.Data, m.URI, string(value(m.MimeType)), "video")
	default:
		return provider.Content{}, "", fmt.Errorf("gemini: unsupported output content %q", content.Type)
	}
}

func mediaContent(data, uri *string, mediaType, kind string) (provider.Content, string, error) {
	file, err := interactionFile(data, uri, mediaType)
	if err != nil {
		return provider.Content{}, kind, err
	}
	return provider.Content{File: file}, kind, nil
}

func streamedFile(data, uri *string, mediaType, kind string, step *interactionStep) (*provider.File, error) {
	if mediaType != "" {
		step.media[kind] = mediaType
	}
	return interactionFile(data, uri, step.media[kind])
}

func interactionFile(data, uri *string, mediaType string) (*provider.File, error) {
	if data == nil && uri == nil {
		return nil, nil
	}
	file := &provider.File{ContentType: mediaType}
	if data != nil {
		decoded, err := base64.StdEncoding.DecodeString(*data)
		if err != nil {
			return nil, fmt.Errorf("gemini: invalid base64 media: %w", err)
		}
		file.Content = decoded
	} else {
		// URI-only files follow the existing provider.File convention.
		file.Content = []byte(*uri)
	}
	return file, nil
}
