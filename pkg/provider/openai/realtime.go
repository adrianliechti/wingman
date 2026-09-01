package openai

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var _ provider.Realtime = (*Realtime)(nil)

type realtimeDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

// Realtime adapts OpenAI's native WebSocket protocol to the provider-neutral
// realtime session contract. Keeping this translation here prevents an OpenAI
// server handler from becoming a special-case raw proxy.
type Realtime struct {
	*Config

	dial realtimeDialer
}

func NewRealtime(rawURL, model string, options ...Option) (*Realtime, error) {
	cfg := &Config{
		url:    rawURL,
		model:  model,
		client: provider.DefaultClient,
	}

	for _, option := range options {
		option(cfg)
	}

	if cfg.token == "" {
		cfg.token = os.Getenv("OPENAI_API_KEY")
	}
	cfg.init()

	dialer := websocketDialer(cfg.client)

	return &Realtime{
		Config: cfg,
		dial: func(ctx context.Context, target string, headers http.Header) (*websocket.Conn, *http.Response, error) {
			return dialer.DialContext(ctx, target, headers)
		},
	}, nil
}

func (r *Realtime) Defaults() provider.RealtimeOptions {
	return provider.RealtimeOptions{
		Voice: "marin",
		InputAudio: provider.RealtimeAudioFormat{
			Encoding:   provider.RealtimeAudioPCM,
			SampleRate: 24000,
			SampleSize: 16,
			Channels:   1,
		},
		OutputAudio: provider.RealtimeAudioFormat{
			Encoding:   provider.RealtimeAudioPCM,
			SampleRate: 24000,
			SampleSize: 16,
			Channels:   1,
		},
		ToolChoice:       provider.ToolChoiceAuto,
		OutputModalities: []provider.RealtimeModality{provider.RealtimeModalityAudio},
		TurnDetection: &provider.RealtimeTurnDetection{
			Type:              provider.RealtimeTurnDetectionServer,
			CreateResponse:    true,
			InterruptResponse: true,
		},
		OutputTranscription: true,
	}
}

func (r *Realtime) Capabilities() provider.RealtimeCapabilities {
	return provider.RealtimeCapabilities{
		SessionUpdates:      true,
		AudioBufferClearing: true,
		ResponseRequests:    true,
		Interruption:        true,
		InputActivityEvents: true,
	}
}

func (r *Realtime) Connect(ctx context.Context, options *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	resolved := mergeOpenAIRealtimeOptions(r.Defaults(), options)
	if err := validateOpenAIRealtimeOptions(resolved); err != nil {
		return nil, err
	}

	target, err := r.websocketURL()
	if err != nil {
		return nil, err
	}

	headers := make(http.Header)
	if r.token != "" {
		if r.isAzure() {
			headers.Set("api-key", r.token)
		} else {
			headers.Set("Authorization", "Bearer "+r.token)
		}
	}

	conn, response, err := r.dial(ctx, target, headers)
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("openai realtime: websocket handshake returned %s: %w", response.Status, err)
		}
		return nil, fmt.Errorf("openai realtime: %w", err)
	}
	conn.SetReadLimit(32 << 20)

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &openAIRealtimeSession{
		ctx:         sessionCtx,
		cancel:      cancel,
		conn:        conn,
		events:      make(chan provider.RealtimeEvent, 128),
		ready:       make(chan error, 1),
		options:     resolved,
		contents:    make(map[string]provider.RealtimeContentType),
		transcripts: make(map[string]bool),
	}
	go session.receive()

	select {
	case err := <-session.ready:
		if err != nil {
			_ = session.Close()
			return nil, err
		}
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
		_ = session.Close()
		return nil, errors.New("openai realtime: timed out waiting for session.created")
	}

	if err := session.Update(ctx, &resolved); err != nil {
		_ = session.Close()
		return nil, err
	}
	for _, message := range resolved.History {
		if message.Text() == "" {
			continue
		}
		if err := session.SendMessage(ctx, message); err != nil {
			_ = session.Close()
			return nil, err
		}
	}

	return session, nil
}

func (r *Realtime) websocketURL() (string, error) {
	u, err := url.Parse(r.url)
	if err != nil {
		return "", fmt.Errorf("openai realtime: invalid base URL: %w", err)
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("openai realtime: unsupported URL scheme %q", u.Scheme)
	}

	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/realtime") {
		u.Path = strings.TrimRight(u.Path, "/") + "/realtime"
	}
	query := u.Query()
	query.Set("model", r.model)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func websocketDialer(client *http.Client) *websocket.Dialer {
	dialer := *websocket.DefaultDialer
	if client == nil {
		return &dialer
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		return &dialer
	}

	dialer.Proxy = transport.Proxy
	dialer.NetDialContext = transport.DialContext
	dialer.HandshakeTimeout = transport.TLSHandshakeTimeout
	if transport.TLSClientConfig != nil {
		dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
	} else {
		dialer.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &dialer
}

func mergeOpenAIRealtimeOptions(defaults provider.RealtimeOptions, options *provider.RealtimeOptions) provider.RealtimeOptions {
	if options == nil {
		return defaults
	}

	result := *options
	result.Tools = slices.Clone(options.Tools)
	result.History = slices.Clone(options.History)
	result.OutputModalities = slices.Clone(options.OutputModalities)
	if options.TurnDetection != nil {
		turnDetection := *options.TurnDetection
		result.TurnDetection = &turnDetection
	}
	if options.InputTranscription != nil {
		transcription := *options.InputTranscription
		result.InputTranscription = &transcription
	}
	if options.Truncation != nil {
		truncation := *options.Truncation
		result.Truncation = &truncation
	}

	if result.Voice == "" {
		result.Voice = defaults.Voice
	}
	if result.InputAudio.Encoding == "" {
		result.InputAudio = defaults.InputAudio
	}
	if result.OutputAudio.Encoding == "" {
		result.OutputAudio = defaults.OutputAudio
	}
	if result.ToolChoice == "" {
		result.ToolChoice = defaults.ToolChoice
	}
	if len(result.OutputModalities) == 0 {
		result.OutputModalities = slices.Clone(defaults.OutputModalities)
	}
	return result
}

func validateOpenAIRealtimeOptions(options provider.RealtimeOptions) error {
	for name, format := range map[string]provider.RealtimeAudioFormat{
		"input": options.InputAudio, "output": options.OutputAudio,
	} {
		if format.SampleSize != 16 || format.Channels != 1 {
			return fmt.Errorf("openai realtime: %s audio must be mono 16-bit", name)
		}
		switch format.Encoding {
		case provider.RealtimeAudioPCM:
			if format.SampleRate != 24000 {
				return fmt.Errorf("openai realtime: %s PCM audio must use 24000 Hz", name)
			}
		case provider.RealtimeAudioPCMU, provider.RealtimeAudioPCMA:
			if format.SampleRate != 8000 {
				return fmt.Errorf("openai realtime: %s G.711 audio must use 8000 Hz", name)
			}
		default:
			return fmt.Errorf("openai realtime: unsupported %s audio encoding %q", name, format.Encoding)
		}
	}

	if len(options.OutputModalities) != 1 {
		return errors.New("openai realtime: exactly one output modality is required")
	}
	return nil
}

type openAIRealtimeSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	conn   *websocket.Conn

	events chan provider.RealtimeEvent
	ready  chan error

	sendMu    sync.Mutex
	stateMu   sync.Mutex
	closeOnce sync.Once
	closed    atomic.Bool

	options        provider.RealtimeOptions
	contents       map[string]provider.RealtimeContentType
	transcripts    map[string]bool
	activeResponse string
}

var _ provider.RealtimeSession = (*openAIRealtimeSession)(nil)

func (s *openAIRealtimeSession) Update(ctx context.Context, options *provider.RealtimeOptions) error {
	if options == nil {
		return errors.New("openai realtime: session options are required")
	}
	if err := validateOpenAIRealtimeOptions(*options); err != nil {
		return err
	}

	s.stateMu.Lock()
	s.options = *options
	s.stateMu.Unlock()

	return s.send(ctx, map[string]any{
		"type":     "session.update",
		"event_id": realtimeID("event"),
		"session":  openAISessionObject(*options),
	})
}

func openAISessionObject(options provider.RealtimeOptions) map[string]any {
	tools := make([]map[string]any, 0, len(options.Tools))
	for _, tool := range options.Tools {
		if tool.Name == "" {
			continue
		}
		tools = append(tools, map[string]any{
			"type": "function", "name": tool.Name,
			"description": tool.Description, "parameters": tool.Parameters,
		})
	}

	var maxTokens any = "inf"
	if options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	var turnDetection any
	if options.TurnDetection != nil {
		detectionType := options.TurnDetection.Type
		if detectionType == "" {
			detectionType = provider.RealtimeTurnDetectionServer
		}
		config := map[string]any{
			"type":               string(detectionType),
			"create_response":    options.TurnDetection.CreateResponse,
			"interrupt_response": options.TurnDetection.InterruptResponse,
		}
		if options.TurnDetection.Eagerness != "" {
			config["eagerness"] = options.TurnDetection.Eagerness
		}
		if options.TurnDetection.Threshold != nil {
			config["threshold"] = *options.TurnDetection.Threshold
		}
		if options.TurnDetection.PrefixPadding > 0 {
			config["prefix_padding_ms"] = options.TurnDetection.PrefixPadding.Milliseconds()
		}
		if options.TurnDetection.SilenceDuration > 0 {
			config["silence_duration_ms"] = options.TurnDetection.SilenceDuration.Milliseconds()
		}
		if options.TurnDetection.IdleTimeout > 0 {
			config["idle_timeout_ms"] = options.TurnDetection.IdleTimeout.Milliseconds()
		}
		turnDetection = config
	}
	var transcription any
	if options.InputTranscription != nil {
		model := options.InputTranscription.Model
		if model == "" {
			model = "gpt-live-transcribe"
		}
		config := map[string]any{"model": model}
		if options.InputTranscription.Language != "" {
			config["language"] = options.InputTranscription.Language
		}
		if options.InputTranscription.Prompt != "" {
			config["prompt"] = options.InputTranscription.Prompt
		}
		transcription = config
	}
	var noiseReduction any
	if options.InputNoiseReduction != "" {
		noiseReduction = map[string]any{"type": string(options.InputNoiseReduction)}
	}

	modalities := make([]string, len(options.OutputModalities))
	for i, modality := range options.OutputModalities {
		modalities[i] = string(modality)
	}

	result := map[string]any{
		"type":              "realtime",
		"output_modalities": modalities,
		"instructions":      options.Instructions,
		"max_output_tokens": maxTokens,
		"tools":             tools,
		"tool_choice":       openAIToolChoice(options.ToolChoice),
		"audio": map[string]any{
			"input": map[string]any{
				"format":          openAIAudioFormat(options.InputAudio),
				"transcription":   transcription,
				"noise_reduction": noiseReduction,
				"turn_detection":  turnDetection,
			},
			"output": map[string]any{
				"format": openAIAudioFormat(options.OutputAudio),
				"voice":  options.Voice,
			},
		},
	}
	if options.Truncation != nil {
		if options.Truncation.Disabled {
			result["truncation"] = "disabled"
		} else if options.Truncation.RetentionRatio != nil {
			truncation := map[string]any{
				"type":            "retention_ratio",
				"retention_ratio": *options.Truncation.RetentionRatio,
			}
			if options.Truncation.PostInstructionTokens != nil {
				truncation["token_limits"] = map[string]any{"post_instructions": *options.Truncation.PostInstructionTokens}
			}
			result["truncation"] = truncation
		}
	}
	return result
}

func openAIAudioFormat(format provider.RealtimeAudioFormat) map[string]any {
	typeName := "audio/pcm"
	switch format.Encoding {
	case provider.RealtimeAudioPCMU:
		typeName = "audio/pcmu"
	case provider.RealtimeAudioPCMA:
		typeName = "audio/pcma"
	}
	result := map[string]any{"type": typeName}
	if format.Encoding == provider.RealtimeAudioPCM {
		result["rate"] = format.SampleRate
	}
	return result
}

func openAIToolChoice(choice provider.ToolChoice) string {
	switch choice {
	case provider.ToolChoiceAny:
		return "required"
	case provider.ToolChoiceNone:
		return "none"
	default:
		return "auto"
	}
}

func (s *openAIRealtimeSession) SendAudio(ctx context.Context, audio []byte) error {
	if len(audio) == 0 {
		return nil
	}
	return s.send(ctx, map[string]any{
		"type": "input_audio_buffer.append", "event_id": realtimeID("event"),
		"audio": base64.StdEncoding.EncodeToString(audio),
	})
}

func (s *openAIRealtimeSession) CommitAudio(ctx context.Context) error {
	return s.send(ctx, map[string]any{"type": "input_audio_buffer.commit", "event_id": realtimeID("event")})
}

func (s *openAIRealtimeSession) ClearAudio(ctx context.Context) error {
	return s.send(ctx, map[string]any{"type": "input_audio_buffer.clear", "event_id": realtimeID("event")})
}

func (s *openAIRealtimeSession) SendMessage(ctx context.Context, message provider.Message) error {
	text := message.Text()
	if text == "" {
		return errors.New("openai realtime: text message is empty")
	}

	contentType := "input_text"
	if message.Role == provider.MessageRoleAssistant {
		contentType = "output_text"
	}
	return s.send(ctx, map[string]any{
		"type": "conversation.item.create", "event_id": realtimeID("event"),
		"item": map[string]any{
			"type": "message", "role": string(message.Role),
			"content": []map[string]any{{"type": contentType, "text": text}},
		},
	})
}

func (s *openAIRealtimeSession) SendToolResult(ctx context.Context, id, output string) error {
	if id == "" {
		return errors.New("openai realtime: tool call id is required")
	}
	return s.send(ctx, map[string]any{
		"type": "conversation.item.create", "event_id": realtimeID("event"),
		"item": map[string]any{"type": "function_call_output", "call_id": id, "output": output},
	})
}

func (s *openAIRealtimeSession) TruncateOutput(ctx context.Context, itemID string, audioEnd time.Duration) error {
	if itemID == "" {
		return errors.New("openai realtime: output item id is required")
	}
	return s.send(ctx, map[string]any{
		"type": "conversation.item.truncate", "event_id": realtimeID("event"),
		"item_id": itemID, "content_index": 0, "audio_end_ms": audioEnd.Milliseconds(),
	})
}

func (s *openAIRealtimeSession) Respond(ctx context.Context, options *provider.RealtimeResponseOptions) error {
	event := map[string]any{"type": "response.create", "event_id": realtimeID("event")}
	if options != nil && len(options.OutputModalities) > 0 {
		modalities := make([]string, len(options.OutputModalities))
		for i, modality := range options.OutputModalities {
			modalities[i] = string(modality)
		}
		event["response"] = map[string]any{"output_modalities": modalities}
	}
	return s.send(ctx, event)
}

func (s *openAIRealtimeSession) Interrupt(ctx context.Context) error {
	return s.send(ctx, map[string]any{"type": "response.cancel", "event_id": realtimeID("event")})
}

func (s *openAIRealtimeSession) Events() <-chan provider.RealtimeEvent { return s.events }

func (s *openAIRealtimeSession) Close() error {
	var result error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.cancel()
		deadline := time.Now().Add(time.Second)
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
		result = s.conn.Close()
	})
	return result
}

func (s *openAIRealtimeSession) send(ctx context.Context, event any) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.closed.Load() {
		return errors.New("openai realtime: session is closed")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(deadline)
		defer s.conn.SetWriteDeadline(time.Time{})
	}
	if err := s.conn.WriteJSON(event); err != nil {
		return fmt.Errorf("openai realtime: write event: %w", err)
	}
	return nil
}

func (s *openAIRealtimeSession) receive() {
	defer close(s.events)
	ready := false
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if !ready {
				s.signalReady(fmt.Errorf("openai realtime: read session.created: %w", err))
			}
			if !s.closed.Load() {
				s.emit(provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: fmt.Errorf("openai realtime: read event: %w", err)})
			}
			return
		}

		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			s.emit(provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: fmt.Errorf("openai realtime: decode event: %w", err)})
			continue
		}
		if header.Type == "session.created" && !ready {
			ready = true
			s.signalReady(nil)
			continue
		}

		for _, event := range s.translate(data) {
			s.emit(event)
		}
	}
}

func (s *openAIRealtimeSession) signalReady(err error) {
	select {
	case s.ready <- err:
	default:
	}
}

func (s *openAIRealtimeSession) emit(event provider.RealtimeEvent) {
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

type openAIRealtimeEvent struct {
	Type           string `json:"type"`
	EventID        string `json:"event_id"`
	ItemID         string `json:"item_id"`
	PreviousItemID string `json:"previous_item_id"`
	ResponseID     string `json:"response_id"`
	ContentIndex   int    `json:"content_index"`
	AudioStartMS   int64  `json:"audio_start_ms"`
	AudioEndMS     int64  `json:"audio_end_ms"`
	Delta          string `json:"delta"`
	Transcript     string `json:"transcript"`
	Response       struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		StatusDetails struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"status_details"`
		Usage *openAIRealtimeUsage `json:"usage"`
	} `json:"response"`
	Part struct {
		Type string `json:"type"`
	} `json:"part"`
	Item struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Role      string `json:"role"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	CallID     string `json:"call_id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	RateLimits []struct {
		Name         string  `json:"name"`
		Limit        int     `json:"limit"`
		Remaining    int     `json:"remaining"`
		ResetSeconds float64 `json:"reset_seconds"`
	} `json:"rate_limits"`
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
		EventID string `json:"event_id"`
	} `json:"error"`
}

type openAIRealtimeUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	InputTokenDetails struct {
		TextTokens  int `json:"text_tokens"`
		AudioTokens int `json:"audio_tokens"`
	} `json:"input_token_details"`
	OutputTokenDetails struct {
		TextTokens  int `json:"text_tokens"`
		AudioTokens int `json:"audio_tokens"`
	} `json:"output_token_details"`
}

func (s *openAIRealtimeSession) translate(data []byte) []provider.RealtimeEvent {
	var value openAIRealtimeEvent
	if err := json.Unmarshal(data, &value); err != nil {
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventError, Err: fmt.Errorf("openai realtime: decode event: %w", err)}}
	}

	contentID := fmt.Sprintf("%s:%d", value.ItemID, value.ContentIndex)
	switch value.Type {
	case "response.created":
		s.activeResponse = value.Response.ID
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventResponseStarted, ResponseID: value.Response.ID}}

	case "response.content_part.added":
		contentType := openAIContentType(value.Part.Type)
		s.contents[contentID] = contentType
		return []provider.RealtimeEvent{{
			Type: provider.RealtimeEventContentStarted, ResponseID: value.ResponseID,
			ContentID: contentID, ItemID: value.ItemID, ContentType: contentType,
			Role: provider.MessageRoleAssistant, Stage: provider.RealtimeGenerationFinal,
		}}

	case "response.output_text.delta", "response.output_audio_transcript.delta":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventTextDelta, ResponseID: value.ResponseID, ContentID: contentID, ItemID: value.ItemID, Text: value.Delta}}

	case "response.output_audio.delta":
		audio, err := base64.StdEncoding.DecodeString(value.Delta)
		if err != nil {
			return []provider.RealtimeEvent{{Type: provider.RealtimeEventError, Err: fmt.Errorf("openai realtime: decode audio: %w", err)}}
		}
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventAudioDelta, ResponseID: value.ResponseID, ContentID: contentID, ItemID: value.ItemID, Audio: audio}}

	case "response.content_part.done":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventContentDone, ResponseID: value.ResponseID, ContentID: contentID, ItemID: value.ItemID, ContentType: s.contents[contentID]}}

	case "response.function_call_arguments.done":
		id := value.ItemID
		if id == "" {
			id = value.CallID
		}
		return []provider.RealtimeEvent{
			{Type: provider.RealtimeEventContentStarted, ResponseID: value.ResponseID, ContentID: id, ItemID: value.ItemID, ContentType: provider.RealtimeContentTool, Role: provider.MessageRoleAssistant},
			{Type: provider.RealtimeEventToolCall, ResponseID: value.ResponseID, ContentID: id, ItemID: value.ItemID, ToolCall: &provider.ToolCall{ID: value.CallID, Name: value.Name, Arguments: value.Arguments}},
			{Type: provider.RealtimeEventContentDone, ResponseID: value.ResponseID, ContentID: id, ItemID: value.ItemID, ContentType: provider.RealtimeContentTool},
		}

	case "conversation.item.input_audio_transcription.delta":
		var events []provider.RealtimeEvent
		if !s.transcripts[contentID] {
			s.transcripts[contentID] = true
			events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: contentID, ItemID: value.ItemID, ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser, Stage: provider.RealtimeGenerationFinal})
		}
		events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: contentID, ItemID: value.ItemID, Text: value.Delta})
		return events

	case "conversation.item.input_audio_transcription.completed":
		var events []provider.RealtimeEvent
		if !s.transcripts[contentID] {
			events = append(events,
				provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: contentID, ItemID: value.ItemID, ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser, Stage: provider.RealtimeGenerationFinal},
				provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: contentID, ItemID: value.ItemID, Text: value.Transcript},
			)
		}
		delete(s.transcripts, contentID)
		events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventContentDone, ContentID: contentID, ItemID: value.ItemID, ContentType: provider.RealtimeContentText})
		return events

	case "input_audio_buffer.speech_started":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputSpeechStarted, ItemID: value.ItemID, AudioStart: time.Duration(value.AudioStartMS) * time.Millisecond}}
	case "input_audio_buffer.speech_stopped":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputSpeechStopped, ItemID: value.ItemID, AudioEnd: time.Duration(value.AudioEndMS) * time.Millisecond}}
	case "input_audio_buffer.committed":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputCommitted, ItemID: value.ItemID, PreviousItemID: value.PreviousItemID}}
	case "input_audio_buffer.cleared":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputCleared}}

	case "rate_limits.updated":
		limits := make([]provider.RealtimeRateLimit, 0, len(value.RateLimits))
		for _, limit := range value.RateLimits {
			limits = append(limits, provider.RealtimeRateLimit{Name: limit.Name, Limit: limit.Limit, Remaining: limit.Remaining, ResetAfter: time.Duration(limit.ResetSeconds * float64(time.Second))})
		}
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventRateLimits, RateLimits: limits}}

	case "response.done":
		var events []provider.RealtimeEvent
		if value.Response.Usage != nil {
			events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventUsage, ResponseID: value.Response.ID, Usage: convertOpenAIRealtimeUsage(value.Response.Usage)})
		}
		stopReason := value.Response.Status
		if value.Response.StatusDetails.Reason != "" {
			stopReason = value.Response.StatusDetails.Reason
		}
		if value.Response.Status == "cancelled" {
			events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventInterrupted, ResponseID: value.Response.ID})
		}
		events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventResponseDone, ResponseID: value.Response.ID, StopReason: stopReason})
		s.activeResponse = ""
		return events

	case "conversation.item.input_audio_transcription.failed":
		message := value.Error.Message
		if message == "" {
			message = value.Type
		}
		return []provider.RealtimeEvent{{
			Type: provider.RealtimeEventInputTranscriptionFailed, ContentID: contentID,
			ItemID: value.ItemID, Err: errors.New(message),
		}}

	case "error":
		message := value.Error.Message
		if message == "" {
			message = value.Type
		}
		return []provider.RealtimeEvent{{
			Type: provider.RealtimeEventError,
			Error: &provider.RealtimeError{
				Type: value.Error.Type, Code: value.Error.Code, Message: message,
				Param: value.Error.Param, EventID: value.Error.EventID,
			},
			Err: errors.New(message),
		}}
	}

	return nil
}

func openAIContentType(value string) provider.RealtimeContentType {
	switch value {
	case "audio":
		return provider.RealtimeContentAudio
	default:
		return provider.RealtimeContentText
	}
}

func convertOpenAIRealtimeUsage(value *openAIRealtimeUsage) *provider.RealtimeUsage {
	return &provider.RealtimeUsage{
		InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
		InputTextTokens: value.InputTokenDetails.TextTokens, InputAudioTokens: value.InputTokenDetails.AudioTokens,
		OutputTextTokens: value.OutputTokenDetails.TextTokens, OutputAudioTokens: value.OutputTokenDetails.AudioTokens,
	}
}

func realtimeID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
