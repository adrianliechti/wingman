package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/gorilla/websocket"
)

const maxAudioEventSize = 15 << 20

type openAISession struct {
	conn         *websocket.Conn
	provider     provider.Realtime
	capabilities provider.RealtimeCapabilities
	model        string

	id             string
	conversationID string

	config sessionConfig

	upstream       provider.RealtimeSession
	upstreamEvents <-chan provider.RealtimeEvent
	providerFailed bool

	queued []provider.Message

	manualAudio bytes.Buffer

	pendingInputItemID string
	turnAudioBytes     int64
	totalAudioBytes    int64
	speechActive       bool
	inputCommitted     bool

	nextOutputModalities   []string
	nextResponseMetadata   map[string]string
	activeOutputModalities []string

	response *responseState
	contents map[string]*contentState
}

func newOpenAISession(conn *websocket.Conn, realtime provider.Realtime, model string) *openAISession {
	return &openAISession{
		conn:         conn,
		provider:     realtime,
		capabilities: realtime.Capabilities(),
		model:        model,

		id:             newID("sess"),
		conversationID: newID("conv"),

		config: newSessionConfig(realtime.Defaults()),

		contents: make(map[string]*contentState),
	}
}

type readResult struct {
	data []byte
	err  error
}

func (s *openAISession) run(ctx context.Context) error {
	if err := s.write(map[string]any{
		"type":    "session.created",
		"session": s.config.object(s.id, s.model),
	}); err != nil {
		return err
	}
	if err := s.write(map[string]any{
		"type": "conversation.created",
		"conversation": map[string]any{
			"id": s.conversationID, "object": "realtime.conversation",
		},
	}); err != nil {
		return err
	}

	reads := make(chan readResult)
	go s.read(ctx, reads)

	defer func() {
		if s.upstream != nil {
			_ = s.upstream.Close()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case result := <-reads:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) || websocket.IsCloseError(result.err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return nil
				}

				return result.err
			}

			if err := s.handleClientEvent(ctx, result.data); err != nil {
				return err
			}

		case event, ok := <-s.upstreamEvents:
			if !ok {
				s.upstreamEvents = nil
				if s.providerFailed {
					return errors.New("the realtime provider stream closed after reporting an error")
				}
				if err := s.writeError("", "provider_stream_closed", "The realtime provider stream closed", ""); err != nil {
					return err
				}
				return errors.New("the realtime provider stream closed unexpectedly")
			}

			if err := s.handleProviderEvent(event); err != nil {
				return err
			}
		}
	}
}

func (s *openAISession) read(ctx context.Context, results chan<- readResult) {
	for {
		messageType, data, err := s.conn.ReadMessage()
		if err == nil && messageType != websocket.TextMessage {
			err = errors.New("realtime client messages must be JSON text frames")
		}

		select {
		case results <- readResult{data: data, err: err}:
		case <-ctx.Done():
			return
		}

		if err != nil {
			return
		}
	}
}

func (s *openAISession) handleClientEvent(ctx context.Context, data []byte) error {
	var event clientEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return s.writeError("", "invalid_json", "Client event is not valid JSON", "")
	}

	var err error
	switch event.Type {
	case "session.update":
		err = s.updateSession(ctx, event)
		if err == nil {
			return s.write(map[string]any{
				"type":    "session.updated",
				"session": s.config.object(s.id, s.model),
			})
		}

	case "input_audio_buffer.append":
		err = s.appendAudio(ctx, event.Audio)

	case "input_audio_buffer.commit":
		err = s.commitAudio(ctx)

	case "input_audio_buffer.clear":
		nativeAck := s.upstream != nil && s.capabilities.AudioBufferClearing && s.turnAudioBytes > 0
		err = s.clearAudio(ctx)
		if err == nil && !nativeAck {
			return s.write(map[string]any{"type": "input_audio_buffer.cleared"})
		}

	case "conversation.item.create":
		err = s.createConversationItem(ctx, event.Item, event.PreviousItemID)

	case "conversation.item.truncate":
		if s.upstream != nil {
			err = s.upstream.TruncateOutput(ctx, event.ItemID, time.Duration(event.AudioEndMS)*time.Millisecond)
			if err != nil {
				break
			}
		}
		return s.write(map[string]any{
			"type":          "conversation.item.truncated",
			"item_id":       event.ItemID,
			"content_index": event.ContentIndex,
			"audio_end_ms":  event.AudioEndMS,
		})

	case "response.create":
		err = s.createResponse(ctx, event.Response)

	case "response.cancel":
		if s.upstream == nil {
			err = errors.New("there is no active response to cancel")
		} else if event.ResponseID != "" && s.response != nil && event.ResponseID != s.response.id {
			err = fmt.Errorf("response %q is not active", event.ResponseID)
		} else {
			err = s.upstream.Interrupt(ctx)
		}

	case "conversation.item.delete", "conversation.item.retrieve", "output_audio_buffer.clear":
		err = fmt.Errorf("client event %q is not supported by this realtime provider", event.Type)

	case "":
		err = errors.New("client event type is required")

	default:
		err = fmt.Errorf("unsupported client event type %q", event.Type)
	}

	if err != nil {
		return s.writeError(event.EventID, "invalid_request", err.Error(), "type")
	}

	return nil
}

func (s *openAISession) updateSession(ctx context.Context, event clientEvent) error {
	updated := s.config
	if err := updated.apply(event.Session); err != nil {
		return err
	}

	if s.upstream != nil {
		if !s.capabilities.SessionUpdates {
			return provider.UnsupportedRealtimeOperation("session update after the provider stream has started")
		}
		options := updated.options(nil)
		if err := s.upstream.Update(ctx, &options); err != nil {
			return err
		}
	}

	s.config = updated
	return nil
}

func (s *openAISession) appendAudio(ctx context.Context, encoded string) error {
	if encoded == "" {
		return errors.New("audio is required")
	}

	if len(encoded) > base64.StdEncoding.EncodedLen(maxAudioEventSize) {
		return errors.New("audio event exceeds the 15 MiB limit")
	}

	audio, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return errors.New("audio must be base64 encoded")
	}
	if len(audio) > maxAudioEventSize {
		return errors.New("audio event exceeds the 15 MiB limit")
	}

	if s.config.TurnDetection == nil {
		_, err := s.manualAudio.Write(audio)
		return err
	}

	if err := s.ensureProvider(ctx, s.queued); err != nil {
		return err
	}
	s.queued = nil

	if err := s.upstream.SendAudio(ctx, audio); err != nil {
		return err
	}

	s.turnAudioBytes += int64(len(audio))
	s.totalAudioBytes += int64(len(audio))
	return nil
}

func (s *openAISession) commitAudio(ctx context.Context) error {
	buffered := s.manualAudio.Len()
	if buffered == 0 && s.turnAudioBytes == 0 {
		return errors.New("input audio buffer is empty")
	}

	if s.upstream == nil {
		if err := s.ensureProvider(ctx, s.queued); err != nil {
			return err
		}
		s.queued = nil
	}

	if buffered > 0 {
		s.pendingInputItemID = newID("item")
		s.inputCommitted = false

		if err := s.upstream.SendAudio(ctx, s.manualAudio.Bytes()); err != nil {
			return err
		}

		s.turnAudioBytes += int64(buffered)
		s.totalAudioBytes += int64(buffered)
		s.manualAudio.Reset()
	}

	if err := s.upstream.CommitAudio(ctx); err != nil {
		return err
	}

	if s.pendingInputItemID == "" {
		s.pendingInputItemID = newID("item")
	}

	if !s.capabilities.InputActivityEvents && s.config.TurnDetection != nil && s.speechActive {
		if err := s.write(map[string]any{
			"type":         "input_audio_buffer.speech_stopped",
			"audio_end_ms": s.audioMilliseconds(s.totalAudioBytes),
			"item_id":      s.pendingInputItemID,
		}); err != nil {
			return err
		}
		s.speechActive = false
	}

	if !s.capabilities.InputActivityEvents {
		if err := s.write(map[string]any{
			"type":             "input_audio_buffer.committed",
			"previous_item_id": nil,
			"item_id":          s.pendingInputItemID,
		}); err != nil {
			return err
		}
	}

	s.inputCommitted = true
	s.turnAudioBytes = 0
	return nil
}

func (s *openAISession) clearAudio(ctx context.Context) error {
	if s.upstream != nil && s.turnAudioBytes > 0 {
		if !s.capabilities.AudioBufferClearing {
			return errors.New("audio already streamed to the realtime provider cannot be cleared")
		}
		if err := s.upstream.ClearAudio(ctx); err != nil {
			return err
		}
	}

	s.manualAudio.Reset()
	s.turnAudioBytes = 0
	s.pendingInputItemID = ""
	s.inputCommitted = false
	s.speechActive = false
	return nil
}

func (s *openAISession) createConversationItem(ctx context.Context, data json.RawMessage, previousItemID string) error {
	var item conversationItem
	if err := json.Unmarshal(data, &item); err != nil {
		return errors.New("conversation item is invalid")
	}
	item.normalize()

	switch item.Type {
	case "function_call_output":
		if item.CallID == "" {
			return errors.New("function call output requires call_id")
		}
		if s.upstream == nil {
			return errors.New("function output received before a realtime provider session exists")
		}
		if err := s.upstream.SendToolResult(ctx, item.CallID, item.Output); err != nil {
			return err
		}
		return s.writeConversationItem(item, previousItemID)

	case "message":
		audio := item.audio()
		if audio != "" {
			if item.Role != "user" {
				return errors.New("only user messages can contain input audio")
			}
			if len(item.Content) != 1 || item.Content[0].Type != "input_audio" {
				return errors.New("conversation input audio cannot be mixed with other content")
			}
			if len(audio) > base64.StdEncoding.EncodedLen(maxAudioEventSize) {
				return errors.New("conversation input audio exceeds the 15 MiB limit")
			}
			decoded, err := base64.StdEncoding.DecodeString(audio)
			if err != nil {
				return errors.New("conversation input audio must be base64 encoded")
			}
			if len(decoded) > maxAudioEventSize {
				return errors.New("conversation input audio exceeds the 15 MiB limit")
			}

			if err := s.ensureProvider(ctx, s.queued); err != nil {
				return err
			}
			s.queued = nil

			if err := s.upstream.SendAudio(ctx, decoded); err != nil {
				return err
			}
			if err := s.upstream.CommitAudio(ctx); err != nil {
				return err
			}

			return s.writeConversationItem(item, previousItemID)
		}

		message, err := item.message()
		if err != nil {
			return err
		}
		if message.Text() == "" {
			return errors.New("conversation text message is empty")
		}

		if s.upstream == nil {
			s.queued = append(s.queued, message)
			return s.writeConversationItem(item, previousItemID)
		}

		if err := s.upstream.SendMessage(ctx, message); err != nil {
			return err
		}
		return s.writeConversationItem(item, previousItemID)

	default:
		return fmt.Errorf("conversation item type %q is unsupported", item.Type)
	}
}

func (s *openAISession) createResponse(ctx context.Context, data json.RawMessage) error {
	var nextOutputModalities []string
	var nextResponseMetadata map[string]string
	if len(data) > 0 && string(data) != "null" && string(data) != "{}" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return errors.New("response configuration is invalid")
		}

		if raw, ok := fields["output_modalities"]; ok {
			var modalities []string
			if err := json.Unmarshal(raw, &modalities); err != nil || len(modalities) != 1 {
				return errors.New("response.output_modalities must contain exactly one modality")
			}
			for _, modality := range modalities {
				if modality != "audio" && modality != "text" {
					return fmt.Errorf("unsupported response output modality %q", modality)
				}
			}
			nextOutputModalities = modalities
		}

		if raw, ok := fields["metadata"]; ok {
			metadata, err := parseMetadata(raw)
			if err != nil {
				return err
			}
			nextResponseMetadata = metadata
		}

		for name := range fields {
			if name != "output_modalities" && name != "metadata" {
				return fmt.Errorf("response override %q is not supported", name)
			}
		}
	}
	s.nextOutputModalities = nextOutputModalities
	s.nextResponseMetadata = nextResponseMetadata

	if s.upstream == nil {
		history := s.queued
		var current *provider.Message
		if len(history) > 0 {
			last := history[len(history)-1]
			if last.Role == provider.MessageRoleUser {
				current = &last
				history = history[:len(history)-1]
			}
		}

		if err := s.ensureProvider(ctx, history); err != nil {
			return err
		}
		s.queued = nil

		if current != nil {
			if err := s.upstream.SendMessage(ctx, *current); err != nil {
				return err
			}
		}
	}

	selectedModalities := s.outputModalities()
	s.activeOutputModalities = slices.Clone(selectedModalities)
	modalities := providerModalities(selectedModalities)
	return s.upstream.Respond(ctx, &provider.RealtimeResponseOptions{OutputModalities: modalities})
}

func (s *openAISession) ensureProvider(ctx context.Context, history []provider.Message) error {
	if s.upstream != nil {
		return nil
	}

	options := s.config.options(history)
	upstream, err := s.provider.Connect(ctx, &options)
	if err != nil {
		return err
	}

	s.upstream = upstream
	s.upstreamEvents = upstream.Events()
	return nil
}

func (s *openAISession) writeConversationItem(item conversationItem, previousItemID string) error {
	if err := s.writeConversationItemStarted(item, previousItemID); err != nil {
		return err
	}

	return s.write(map[string]any{
		"type":             "conversation.item.done",
		"previous_item_id": nullableString(previousItemID),
		"item":             item,
	})
}

func (s *openAISession) writeConversationItemStarted(item any, previousItemID string) error {
	previous := nullableString(previousItemID)
	if err := s.write(map[string]any{
		"type":             "conversation.item.created",
		"previous_item_id": previous,
		"item":             item,
	}); err != nil {
		return err
	}
	if err := s.write(map[string]any{
		"type":             "conversation.item.added",
		"previous_item_id": previous,
		"item":             item,
	}); err != nil {
		return err
	}
	return nil
}

func (s *openAISession) audioMilliseconds(size int64) int64 {
	bytesPerSecond := int64(s.config.InputAudio.SampleRate * s.config.InputAudio.Channels * s.config.InputAudio.SampleSize / 8)
	if bytesPerSecond == 0 {
		return 0
	}

	return size * 1000 / bytesPerSecond
}

func (s *openAISession) outputModalities() []string {
	if len(s.nextOutputModalities) > 0 {
		result := slices.Clone(s.nextOutputModalities)
		s.nextOutputModalities = nil
		return result
	}

	return slices.Clone(s.config.OutputModalities)
}

func (s *openAISession) write(event map[string]any) error {
	if _, ok := event["event_id"]; !ok {
		event["event_id"] = newID("event")
	}

	return s.conn.WriteJSON(event)
}

func (s *openAISession) writeError(clientEventID, code, message, param string) error {
	return s.writeTypedError(clientEventID, "invalid_request_error", code, message, param)
}

func (s *openAISession) writeTypedError(clientEventID, errorType, code, message, param string) error {
	if errorType == "" {
		errorType = "invalid_request_error"
	}
	errorValue := map[string]any{
		"type":     errorType,
		"code":     code,
		"message":  message,
		"param":    nil,
		"event_id": clientEventID,
	}
	if param != "" {
		errorValue["param"] = param
	}

	return s.write(map[string]any{
		"type":  "error",
		"error": errorValue,
	})
}

func containsModality(modalities []string, value string) bool {
	for _, modality := range modalities {
		if strings.EqualFold(modality, value) {
			return true
		}
	}

	return false
}
