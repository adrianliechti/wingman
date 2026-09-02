package realtime

import (
	"encoding/base64"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type contentState struct {
	typeName provider.RealtimeContentType
	role     provider.MessageRole
	stage    provider.RealtimeGenerationStage
	itemID   string
	text     strings.Builder
}

type assistantState struct {
	itemID       string
	outputIndex  int
	contentIndex int
	typeName     provider.RealtimeContentType

	item map[string]any

	text        strings.Builder
	speculative strings.Builder
	final       strings.Builder

	partDone  bool
	itemDone  bool
	audioDone bool
}

type toolState struct {
	contentID   string
	itemID      string
	callID      string
	name        string
	arguments   string
	outputIndex int
	item        map[string]any
	done        bool
}

type responseState struct {
	id         string
	modalities []string
	metadata   map[string]string
	output     []map[string]any

	assistant *assistantState
	tools     map[string]*toolState
	usage     *provider.RealtimeUsage

	interrupted bool
	stopReason  string
}

func (s *openAISession) handleProviderEvent(event provider.RealtimeEvent) error {
	switch event.Type {
	case provider.RealtimeEventResponseStarted:
		return s.startResponse(event.ResponseID)

	case provider.RealtimeEventContentStarted:
		return s.startContent(event)

	case provider.RealtimeEventTextDelta:
		return s.writeTextDelta(event)

	case provider.RealtimeEventAudioDelta:
		return s.writeAudioDelta(event)

	case provider.RealtimeEventToolCall:
		return s.writeToolCall(event)

	case provider.RealtimeEventContentDone:
		return s.finishContent(event)

	case provider.RealtimeEventUsage:
		if event.Usage != nil {
			if err := s.ensureResponse(event.ResponseID); err != nil {
				return err
			}
			s.response.usage = event.Usage
		}
		return nil

	case provider.RealtimeEventInterrupted:
		if s.response != nil {
			s.response.interrupted = true
		}
		// Gemini Live and Nova Sonic report a real barge-in but do not expose
		// OpenAI-style input activity events. Normalize that signal so clients
		// immediately stop and clear queued assistant audio. Never infer speech
		// from packet arrival alone: both providers require continuous audio,
		// including silence.
		if s.config.TurnDetection != nil && !s.capabilities.InputActivityEvents && !s.speechActive {
			s.pendingInputItemID = event.ItemID
			if s.pendingInputItemID == "" {
				s.pendingInputItemID = newID("item")
			}
			s.speechActive = true
			s.inputCommitted = false
			return s.write(map[string]any{
				"type": "input_audio_buffer.speech_started", "item_id": s.pendingInputItemID,
				"audio_start_ms": s.audioMilliseconds(s.totalAudioBytes),
			})
		}
		return nil

	case provider.RealtimeEventResponseDone:
		return s.finishResponse(event)

	case provider.RealtimeEventInputSpeechStarted:
		s.pendingInputItemID = event.ItemID
		if s.pendingInputItemID == "" {
			s.pendingInputItemID = newID("item")
		}
		s.speechActive = true
		s.inputCommitted = false
		return s.write(map[string]any{
			"type": "input_audio_buffer.speech_started", "item_id": s.pendingInputItemID,
			"audio_start_ms": event.AudioStart.Milliseconds(),
		})

	case provider.RealtimeEventInputSpeechStopped:
		itemID := event.ItemID
		if itemID == "" {
			itemID = s.pendingInputItemID
		}
		s.speechActive = false
		return s.write(map[string]any{
			"type": "input_audio_buffer.speech_stopped", "item_id": itemID,
			"audio_end_ms": event.AudioEnd.Milliseconds(),
		})

	case provider.RealtimeEventInputCommitted:
		itemID := event.ItemID
		if itemID == "" {
			itemID = s.pendingInputItemID
		}
		if itemID == "" {
			itemID = newID("item")
		}
		s.pendingInputItemID = itemID
		s.inputCommitted = true
		s.turnAudioBytes = 0
		return s.write(map[string]any{
			"type": "input_audio_buffer.committed", "item_id": itemID,
			"previous_item_id": nullableString(event.PreviousItemID),
		})

	case provider.RealtimeEventInputCleared:
		s.manualAudio.Reset()
		s.turnAudioBytes = 0
		s.pendingInputItemID = ""
		s.inputCommitted = false
		s.speechActive = false
		return s.write(map[string]any{"type": "input_audio_buffer.cleared"})

	case provider.RealtimeEventInputTranscriptionPreview:
		// Gemini interim transcripts are replaceable snapshots, whereas OpenAI's
		// transcription.delta is append-only. Suppress speculative snapshots and
		// emit the authoritative finalized transcript through the normal content
		// events instead of concatenating revisions into corrupted text.
		return nil

	case provider.RealtimeEventInputTranscriptionFailed:
		message := "Input audio transcription failed"
		if event.Err != nil {
			message = event.Err.Error()
		}
		return s.write(map[string]any{
			"type":    "conversation.item.input_audio_transcription.failed",
			"item_id": event.ItemID, "content_index": 0,
			"error": map[string]any{
				"type": "transcription_error", "code": "transcription_failed",
				"message": message, "param": nil,
			},
		})

	case provider.RealtimeEventRateLimits:
		limits := make([]map[string]any, 0, len(event.RateLimits))
		for _, limit := range event.RateLimits {
			limits = append(limits, map[string]any{
				"name": limit.Name, "limit": limit.Limit, "remaining": limit.Remaining,
				"reset_seconds": limit.ResetAfter.Seconds(),
			})
		}
		return s.write(map[string]any{"type": "rate_limits.updated", "rate_limits": limits})

	case provider.RealtimeEventError:
		message := "The realtime provider reported an error"
		diagnostic := message
		if event.Err != nil {
			diagnostic = event.Err.Error()
		}
		s.providerFailed = true
		log.Printf("Realtime provider %q error: %s", s.model, diagnostic)

		errorType := "invalid_request_error"
		code := "provider_error"
		param := ""
		clientEventID := ""
		if event.Error != nil {
			if event.Error.Type != "" {
				errorType = event.Error.Type
			}
			if event.Error.Code != "" {
				code = event.Error.Code
			}
			if event.Error.Message != "" {
				message = event.Error.Message
			}
			param = event.Error.Param
			clientEventID = event.Error.EventID
		}

		return s.writeTypedError(clientEventID, errorType, code, message, param)
	}

	return nil
}

func (s *openAISession) startResponse(id string) error {
	if s.response != nil {
		if id == "" || id == s.response.id {
			return nil
		}
		return fmt.Errorf("realtime provider started response %q before response %q completed", id, s.response.id)
	}
	if id == "" {
		id = newID("resp")
	}
	modalities := slices.Clone(s.activeOutputModalities)
	if len(modalities) == 0 {
		modalities = slices.Clone(s.config.OutputModalities)
	}
	s.activeOutputModalities = nil
	s.response = &responseState{
		id: id, modalities: modalities, metadata: maps.Clone(s.nextResponseMetadata),
		tools: make(map[string]*toolState),
	}
	s.nextResponseMetadata = nil
	return s.write(map[string]any{
		"type": "response.created", "response": s.responseObject("in_progress", nil),
	})
}

func (s *openAISession) ensureResponse(id string) error {
	if s.response != nil {
		return nil
	}
	return s.startResponse(id)
}

func (s *openAISession) startContent(event provider.RealtimeEvent) error {
	contentID := event.ContentID
	if contentID == "" {
		contentID = newID("content")
	}
	itemID := event.ItemID
	if event.Role == provider.MessageRoleUser && s.pendingInputItemID != "" {
		itemID = s.pendingInputItemID
	}
	if itemID == "" {
		itemID = newID("item")
	}
	state := &contentState{
		typeName: event.ContentType, role: event.Role, stage: event.Stage, itemID: itemID,
	}
	if state.stage == "" {
		state.stage = provider.RealtimeGenerationFinal
	}
	s.contents[contentID] = state

	switch state.role {
	case provider.MessageRoleUser:
		s.pendingInputItemID = itemID
		if !s.capabilities.InputActivityEvents && s.speechActive {
			if err := s.write(map[string]any{
				"type": "input_audio_buffer.speech_stopped", "item_id": itemID,
				"audio_end_ms": s.audioMilliseconds(s.totalAudioBytes),
			}); err != nil {
				return err
			}
			if err := s.write(map[string]any{
				"type": "input_audio_buffer.committed", "item_id": itemID,
				"previous_item_id": nil,
			}); err != nil {
				return err
			}
			s.speechActive = false
			s.inputCommitted = true
		}
		item := inputAudioItem(itemID, "in_progress", "")
		return s.writeConversationItemStarted(item, "")

	case provider.MessageRoleAssistant:
		if event.ContentType == provider.RealtimeContentTool {
			return nil
		}
		if err := s.ensureResponse(event.ResponseID); err != nil {
			return err
		}
		_, err := s.ensureAssistant(event)
		return err
	}

	return nil
}

func (s *openAISession) ensureAssistant(event provider.RealtimeEvent) (*assistantState, error) {
	if err := s.ensureResponse(event.ResponseID); err != nil {
		return nil, err
	}
	if s.response.assistant != nil {
		return s.response.assistant, nil
	}

	itemID := event.ItemID
	if itemID == "" {
		itemID = newID("item")
	}
	typeName := provider.RealtimeContentText
	if containsModality(s.response.modalities, "audio") {
		typeName = provider.RealtimeContentAudio
	}
	assistant := &assistantState{
		itemID: itemID, outputIndex: len(s.response.output), contentIndex: 0, typeName: typeName,
	}
	s.response.output = append(s.response.output, nil)
	assistant.item = assistantItem(itemID, "in_progress", typeName, "")
	assistant.item["content"] = []map[string]any{}
	s.response.assistant = assistant

	if err := s.writeConversationItemStarted(assistant.item, ""); err != nil {
		return nil, err
	}
	if err := s.write(map[string]any{
		"type": "response.output_item.added", "response_id": s.response.id,
		"output_index": assistant.outputIndex, "item": assistant.item,
	}); err != nil {
		return nil, err
	}
	part := map[string]any{"type": "text", "text": ""}
	if typeName == provider.RealtimeContentAudio {
		part = map[string]any{"type": "audio", "transcript": ""}
	}
	if err := s.write(map[string]any{
		"type": "response.content_part.added", "response_id": s.response.id,
		"item_id": assistant.itemID, "output_index": assistant.outputIndex,
		"content_index": assistant.contentIndex, "part": part,
	}); err != nil {
		return nil, err
	}
	return assistant, nil
}

func (s *openAISession) writeTextDelta(event provider.RealtimeEvent) error {
	state := s.contents[event.ContentID]
	if state == nil {
		state = &contentState{
			typeName: provider.RealtimeContentText, role: provider.MessageRoleAssistant,
			stage: provider.RealtimeGenerationFinal, itemID: event.ItemID,
		}
		s.contents[event.ContentID] = state
	}
	state.text.WriteString(event.Text)

	if state.role == provider.MessageRoleUser {
		if s.config.Transcription == nil {
			return nil
		}
		return s.write(map[string]any{
			"type":    "conversation.item.input_audio_transcription.delta",
			"item_id": state.itemID, "content_index": 0, "delta": event.Text,
		})
	}

	assistant, err := s.ensureAssistant(provider.RealtimeEvent{
		ResponseID: event.ResponseID, ItemID: state.itemID, ContentType: state.typeName,
	})
	if err != nil {
		return err
	}

	emit := true
	if state.stage == provider.RealtimeGenerationSpeculative {
		assistant.speculative.WriteString(event.Text)
	} else {
		assistant.final.WriteString(event.Text)
		if assistant.speculative.Len() > 0 {
			emit = false
		}
	}
	if emit {
		assistant.text.WriteString(event.Text)
	}
	if !emit || event.Text == "" {
		return nil
	}

	typeName := "response.output_text.delta"
	if assistant.typeName == provider.RealtimeContentAudio {
		typeName = "response.output_audio_transcript.delta"
	}
	return s.write(map[string]any{
		"type": typeName, "response_id": s.response.id, "item_id": assistant.itemID,
		"output_index": assistant.outputIndex, "content_index": assistant.contentIndex,
		"delta": event.Text,
	})
}

func (s *openAISession) writeAudioDelta(event provider.RealtimeEvent) error {
	if err := s.ensureResponse(event.ResponseID); err != nil {
		return err
	}
	if !containsModality(s.response.modalities, "audio") {
		return nil
	}
	assistant, err := s.ensureAssistant(event)
	if err != nil {
		return err
	}
	return s.write(map[string]any{
		"type": "response.output_audio.delta", "response_id": s.response.id,
		"item_id": assistant.itemID, "output_index": assistant.outputIndex,
		"content_index": assistant.contentIndex,
		"delta":         base64.StdEncoding.EncodeToString(event.Audio),
	})
}

func (s *openAISession) writeToolCall(event provider.RealtimeEvent) error {
	if event.ToolCall == nil {
		return nil
	}
	if err := s.ensureResponse(event.ResponseID); err != nil {
		return err
	}
	contentID := event.ContentID
	if contentID == "" {
		contentID = event.ToolCall.ID
	}
	if existing := s.response.tools[contentID]; existing != nil && existing.done {
		return nil
	}
	itemID := event.ItemID
	if itemID == "" {
		itemID = newID("item")
	}
	outputIndex := len(s.response.output)
	item := map[string]any{
		"id": itemID, "object": "realtime.item", "type": "function_call",
		"status": "in_progress", "call_id": event.ToolCall.ID,
		"name": event.ToolCall.Name, "arguments": "",
	}
	tool := &toolState{
		contentID: contentID, itemID: itemID, callID: event.ToolCall.ID,
		name: event.ToolCall.Name, arguments: event.ToolCall.Arguments,
		outputIndex: outputIndex, item: item,
	}
	s.response.tools[contentID] = tool

	if err := s.writeConversationItemStarted(item, ""); err != nil {
		return err
	}
	if err := s.write(map[string]any{
		"type": "response.output_item.added", "response_id": s.response.id,
		"output_index": outputIndex, "item": item,
	}); err != nil {
		return err
	}
	if event.ToolCall.Arguments != "" {
		if err := s.write(map[string]any{
			"type": "response.function_call_arguments.delta", "response_id": s.response.id,
			"item_id": itemID, "output_index": outputIndex, "call_id": event.ToolCall.ID,
			"delta": event.ToolCall.Arguments,
		}); err != nil {
			return err
		}
	}
	if err := s.write(map[string]any{
		"type": "response.function_call_arguments.done", "response_id": s.response.id,
		"item_id": itemID, "output_index": outputIndex, "call_id": event.ToolCall.ID,
		"name": event.ToolCall.Name, "arguments": event.ToolCall.Arguments,
	}); err != nil {
		return err
	}

	item["status"] = "completed"
	item["arguments"] = event.ToolCall.Arguments
	tool.done = true
	s.response.output = append(s.response.output, item)
	if err := s.write(map[string]any{
		"type": "response.output_item.done", "response_id": s.response.id,
		"output_index": outputIndex, "item": item,
	}); err != nil {
		return err
	}
	return s.write(map[string]any{"type": "conversation.item.done", "previous_item_id": nil, "item": item})
}

func (s *openAISession) finishContent(event provider.RealtimeEvent) error {
	state := s.contents[event.ContentID]
	if state == nil {
		return nil
	}
	defer delete(s.contents, event.ContentID)

	if state.role == provider.MessageRoleUser {
		transcript := state.text.String()
		item := inputAudioItem(state.itemID, "completed", transcript)
		if s.config.Transcription != nil {
			if err := s.write(map[string]any{
				"type":    "conversation.item.input_audio_transcription.completed",
				"item_id": state.itemID, "content_index": 0, "transcript": transcript,
				"usage": map[string]any{"type": "tokens", "input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
			}); err != nil {
				return err
			}
		}
		s.pendingInputItemID = ""
		s.inputCommitted = false
		s.speechActive = false
		return s.write(map[string]any{"type": "conversation.item.done", "previous_item_id": nil, "item": item})
	}

	if state.role == provider.MessageRoleAssistant && state.stage == provider.RealtimeGenerationFinal && state.text.Len() > 0 && s.response != nil && s.response.assistant != nil {
		s.response.assistant.final.Reset()
		s.response.assistant.final.WriteString(state.text.String())
	}
	return nil
}

func (s *openAISession) finishResponse(event provider.RealtimeEvent) error {
	if err := s.ensureResponse(event.ResponseID); err != nil {
		return err
	}
	s.response.stopReason = event.StopReason
	if isInterrupted(event.StopReason) {
		s.response.interrupted = true
	}
	if err := s.finishAssistant(); err != nil {
		return err
	}

	status := "completed"
	var statusDetails any
	if s.response.interrupted {
		status = "cancelled"
		statusDetails = map[string]any{"type": "cancelled", "reason": cancellationReason(event.StopReason)}
	} else if isIncomplete(event.StopReason) {
		status = "incomplete"
		reason := "content_filter"
		if strings.Contains(strings.ToLower(event.StopReason), "token") {
			reason = "max_output_tokens"
		}
		statusDetails = map[string]any{"type": "incomplete", "reason": reason}
	} else if isFailed(event.StopReason) {
		status = "failed"
		statusDetails = map[string]any{
			"type":  "failed",
			"error": map[string]any{"type": "server_error", "code": "provider_error"},
		}
	}

	response := s.responseObject(status, statusDetails)
	if err := s.write(map[string]any{"type": "response.done", "response": response}); err != nil {
		return err
	}
	s.response = nil
	s.contents = make(map[string]*contentState)
	return nil
}

func (s *openAISession) finishAssistant() error {
	if s.response == nil || s.response.assistant == nil {
		return nil
	}
	assistant := s.response.assistant
	if assistant.itemDone {
		return nil
	}
	text := assistant.text.String()
	if assistant.final.Len() > 0 {
		text = assistant.final.String()
	}

	if assistant.typeName == provider.RealtimeContentAudio && !assistant.audioDone {
		if err := s.write(map[string]any{
			"type": "response.output_audio.done", "response_id": s.response.id,
			"item_id": assistant.itemID, "output_index": assistant.outputIndex,
			"content_index": assistant.contentIndex,
		}); err != nil {
			return err
		}
		assistant.audioDone = true
		if err := s.write(map[string]any{
			"type": "response.output_audio_transcript.done", "response_id": s.response.id,
			"item_id": assistant.itemID, "output_index": assistant.outputIndex,
			"content_index": assistant.contentIndex, "transcript": text,
		}); err != nil {
			return err
		}
	} else if assistant.typeName == provider.RealtimeContentText {
		if err := s.write(map[string]any{
			"type": "response.output_text.done", "response_id": s.response.id,
			"item_id": assistant.itemID, "output_index": assistant.outputIndex,
			"content_index": assistant.contentIndex, "text": text,
		}); err != nil {
			return err
		}
	}

	part := map[string]any{"type": "text", "text": text}
	if assistant.typeName == provider.RealtimeContentAudio {
		part = map[string]any{"type": "audio", "transcript": text}
	}
	if !assistant.partDone {
		if err := s.write(map[string]any{
			"type": "response.content_part.done", "response_id": s.response.id,
			"item_id": assistant.itemID, "output_index": assistant.outputIndex,
			"content_index": assistant.contentIndex, "part": part,
		}); err != nil {
			return err
		}
		assistant.partDone = true
	}

	assistant.item = assistantItem(assistant.itemID, "completed", assistant.typeName, text)
	s.response.output[assistant.outputIndex] = assistant.item
	if err := s.write(map[string]any{
		"type": "response.output_item.done", "response_id": s.response.id,
		"output_index": assistant.outputIndex, "item": assistant.item,
	}); err != nil {
		return err
	}
	assistant.itemDone = true
	return s.write(map[string]any{"type": "conversation.item.done", "previous_item_id": nil, "item": assistant.item})
}

func (s *openAISession) responseObject(status string, statusDetails any) map[string]any {
	maxTokens := any("inf")
	if s.config.MaxTokens != nil {
		maxTokens = *s.config.MaxTokens
	}
	output := []map[string]any{}
	modalities := slices.Clone(s.config.OutputModalities)
	var usage any
	var metadata any
	if s.response != nil {
		output = slices.Clone(s.response.output)
		modalities = slices.Clone(s.response.modalities)
		usage = usageObject(s.response.usage)
		metadata = maps.Clone(s.response.metadata)
	}
	return map[string]any{
		"id": s.response.id, "object": "realtime.response", "status": status,
		"status_details": statusDetails, "output": output,
		"conversation_id": s.conversationID, "output_modalities": modalities,
		"max_output_tokens": maxTokens, "metadata": metadata, "usage": usage,
		"audio": map[string]any{"output": map[string]any{
			"format": audioFormatObject(s.config.OutputAudio), "voice": s.config.Voice,
		}},
	}
}

func assistantItem(id, status string, contentType provider.RealtimeContentType, text string) map[string]any {
	part := map[string]any{"type": "output_text", "text": text}
	if contentType == provider.RealtimeContentAudio {
		part = map[string]any{"type": "output_audio", "transcript": text}
	}
	return map[string]any{
		"id": id, "object": "realtime.item", "type": "message", "status": status,
		"role": "assistant", "content": []map[string]any{part},
	}
}

func inputAudioItem(id, status, transcript string) map[string]any {
	return map[string]any{
		"id": id, "object": "realtime.item", "type": "message", "status": status,
		"role": "user", "content": []map[string]any{{"type": "input_audio", "transcript": transcript}},
	}
}

func usageObject(usage *provider.RealtimeUsage) any {
	if usage == nil {
		return nil
	}
	return map[string]any{
		"total_tokens": usage.InputTokens + usage.OutputTokens,
		"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens,
		"input_token_details": map[string]any{
			"text_tokens": usage.InputTextTokens, "audio_tokens": usage.InputAudioTokens,
			"cached_tokens": 0,
		},
		"output_token_details": map[string]any{
			"text_tokens": usage.OutputTextTokens, "audio_tokens": usage.OutputAudioTokens,
		},
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isInterrupted(reason string) bool {
	reason = strings.ToLower(reason)
	return strings.Contains(reason, "interrupt") || strings.Contains(reason, "cancel")
}

func cancellationReason(reason string) string {
	reason = strings.ToLower(reason)
	if strings.Contains(reason, "client") || strings.Contains(reason, "cancel") {
		return "client_cancelled"
	}
	return "turn_detected"
}

func isIncomplete(reason string) bool {
	reason = strings.ToLower(reason)
	return strings.Contains(reason, "token") || strings.Contains(reason, "filter") || strings.Contains(reason, "guardrail")
}

func isFailed(reason string) bool {
	reason = strings.ToLower(reason)
	return strings.Contains(reason, "fail") || strings.Contains(reason, "error")
}
