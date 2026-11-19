package openai

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
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
	audio := provider.RealtimeAudioFormat{
		Encoding:   provider.RealtimeAudioPCM,
		SampleRate: 24000,
		SampleSize: 16,
		Channels:   1,
	}

	defaults := provider.RealtimeOptions{
		ToolChoice: provider.ToolChoiceAuto,

		Input: provider.RealtimeInputOptions{
			Audio: audio,

			TurnDetection: &provider.RealtimeTurnDetection{
				Type:              provider.RealtimeTurnDetectionServer,
				CreateResponse:    true,
				InterruptResponse: true,
			},
		},

		Output: provider.RealtimeOutputOptions{
			Modalities: []provider.RealtimeModality{provider.RealtimeModalityAudio},

			Audio: audio,
			Voice: "marin",

			Transcription: true,
		},
	}

	if isTranscriptionModel(r.model) {
		defaults.Input.Transcription = &provider.RealtimeTranscription{Model: r.model}

		// Transcription services disagree: some segment with server VAD, others
		// (gpt-live-transcribe, MAI-Transcribe) only finalize on commit. The
		// service's own default applies until the client chooses.
		defaults.Input.TurnDetection = nil

		defaults.Output.Modalities = []provider.RealtimeModality{provider.RealtimeModalityText}
		defaults.Output.Transcription = false
	}

	if isTranslationModel(r.model) {
		defaults.Input.TurnDetection = nil

		// OpenAI translates to Spanish until the session names a language.
		defaults.Output.Language = "es"
	}

	return defaults
}

func isTranscriptionModel(model string) bool {
	model = strings.ToLower(model)

	return strings.Contains(model, "transcribe") || strings.Contains(model, "whisper")
}

func isTranslationModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "translate")
}

func (r *Realtime) RealtimeCapabilities() provider.RealtimeCapabilities {
	return provider.RealtimeCapabilities{
		TranscriptionOnly:   isTranscriptionModel(r.model),
		TranslationOnly:     isTranslationModel(r.model),
		SessionUpdates:      true,
		AudioBufferClearing: true,
		ResponseRequests:    true,
		Interruption:        true,
		InputActivityEvents: true,
	}
}

func (r *Realtime) Connect(ctx context.Context, options *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	resolved := mergeOpenAIRealtimeOptions(r.Defaults(), options)
	transcription := isTranscriptionModel(r.model)
	translation := isTranslationModel(r.model)
	if err := validateOpenAIRealtimeOptions(resolved, transcription || translation); err != nil {
		return nil, err
	}

	target, err := r.websocketURL()
	if err != nil {
		return nil, err
	}

	headers := make(http.Header)
	if r.token != "" {
		if r.isAzureResource() {
			headers.Set("api-key", r.token)
		} else {
			headers.Set("Authorization", "Bearer "+r.token)
		}
	}

	conn, response, err := r.dial(ctx, target, headers)
	if err != nil {
		if response != nil {
			if response.Body != nil {
				_ = response.Body.Close()
			}
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
		contents:    make(map[string]provider.RealtimeContentType),
		transcripts: make(map[string]*strings.Builder),
		translation: translation,
	}
	if transcription {
		session.transcriptionModel = r.model
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
	if isTranslationModel(r.model) {
		u.Path = strings.TrimRight(u.Path, "/") + "/translations"
	}
	query := u.Query()
	if isTranscriptionModel(r.model) {
		// OpenAI rejects a model parameter next to intent=transcription.
		query.Set("intent", "transcription")
	} else {
		query.Set("model", r.model)
	}
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
	// Gorilla upgrades over HTTP/1.1. The HTTP client's TLS config may have
	// acquired h2 during earlier requests; never offer it for a WebSocket.
	dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return &dialer
}

func mergeOpenAIRealtimeOptions(defaults provider.RealtimeOptions, options *provider.RealtimeOptions) provider.RealtimeOptions {
	if options == nil {
		return defaults
	}

	result := *options
	result.Tools = slices.Clone(options.Tools)
	result.History = slices.Clone(options.History)
	result.Output.Modalities = slices.Clone(options.Output.Modalities)
	if options.Input.TurnDetection != nil {
		turnDetection := *options.Input.TurnDetection
		result.Input.TurnDetection = &turnDetection
	}
	if options.Input.Transcription != nil {
		transcription := *options.Input.Transcription
		result.Input.Transcription = &transcription
	}
	if options.Truncation != nil {
		truncation := *options.Truncation
		result.Truncation = &truncation
	}

	if result.Output.Voice == "" {
		result.Output.Voice = defaults.Output.Voice
	}
	if result.Input.Audio.Encoding == "" {
		result.Input.Audio = defaults.Input.Audio
	}
	if result.Output.Audio.Encoding == "" {
		result.Output.Audio = defaults.Output.Audio
	}
	if result.ToolChoice == "" {
		result.ToolChoice = defaults.ToolChoice
	}
	if len(result.Output.Modalities) == 0 {
		result.Output.Modalities = slices.Clone(defaults.Output.Modalities)
	}
	return result
}

func validateOpenAIRealtimeOptions(options provider.RealtimeOptions, inputOnly bool) error {
	formats := map[string]provider.RealtimeAudioFormat{"input": options.Input.Audio}
	if !inputOnly {
		formats["output"] = options.Output.Audio
	}
	for name, format := range formats {
		if format.Channels != 1 {
			return fmt.Errorf("openai realtime: %s audio must be mono", name)
		}
		switch format.Encoding {
		case provider.RealtimeAudioPCM:
			if format.SampleRate != 24000 || format.SampleSize != 16 {
				return fmt.Errorf("openai realtime: %s PCM audio must be 16-bit at 24000 Hz", name)
			}
		case provider.RealtimeAudioPCMU, provider.RealtimeAudioPCMA:
			if format.SampleRate != 8000 || format.SampleSize != 8 {
				return fmt.Errorf("openai realtime: %s G.711 audio must be 8-bit at 8000 Hz", name)
			}
		default:
			return fmt.Errorf("openai realtime: unsupported %s audio encoding %q", name, format.Encoding)
		}
	}

	if !inputOnly && len(options.Output.Modalities) != 1 {
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
	closeOnce sync.Once
	closed    atomic.Bool

	contents map[string]provider.RealtimeContentType

	// transcripts holds the finalized text of each input transcript in progress.
	transcripts map[string]*strings.Builder

	// MAI omits item ids on commit and completion events; they refer to the
	// oldest transcript item still awaiting one, in order of first output.
	uncommitted []string
	incomplete  []string

	transcriptionModel string
	translation        bool
}

var _ provider.RealtimeSession = (*openAIRealtimeSession)(nil)

func (s *openAIRealtimeSession) Update(ctx context.Context, options *provider.RealtimeOptions) error {
	if options == nil {
		return errors.New("openai realtime: session options are required")
	}
	if err := validateOpenAIRealtimeOptions(*options, s.transcriptionModel != "" || s.translation); err != nil {
		return err
	}

	session := openAISessionObject(*options)
	if s.transcriptionModel != "" {
		session = openAITranscriptionSessionObject(s.transcriptionModel, *options)
	}
	if s.translation {
		session = openAITranslationSessionObject(*options)
	}

	return s.send(ctx, map[string]any{
		"type":     "session.update",
		"event_id": realtimeID("event"),
		"session":  session,
	})
}

func openAITranslationSessionObject(options provider.RealtimeOptions) map[string]any {
	var transcription any
	if options.Input.Transcription != nil {
		transcription = map[string]any{"model": options.Input.Transcription.Model}
	}
	var noiseReduction any
	if options.Input.NoiseReduction != "" {
		noiseReduction = map[string]any{"type": string(options.Input.NoiseReduction)}
	}

	return map[string]any{
		"audio": map[string]any{
			"input":  map[string]any{"transcription": transcription, "noise_reduction": noiseReduction},
			"output": map[string]any{"language": options.Output.Language},
		},
	}
}

func openAITranscriptionSessionObject(model string, options provider.RealtimeOptions) map[string]any {
	if options.Input.Transcription != nil {
		transcription := *options.Input.Transcription
		transcription.Model = model
		options.Input.Transcription = &transcription
	}

	input := openAISessionObject(options)["audio"].(map[string]any)["input"].(map[string]any)
	if detection, ok := input["turn_detection"].(map[string]any); ok {
		delete(detection, "create_response")
		delete(detection, "interrupt_response")
	}

	return map[string]any{
		"type":  "transcription",
		"audio": map[string]any{"input": input},
	}
}

func openAITools(values []provider.Tool) []map[string]any {
	tools := make([]map[string]any, 0, len(values))
	for _, tool := range values {
		if tool.Name == "" {
			continue
		}
		tools = append(tools, map[string]any{
			"type": "function", "name": tool.Name,
			"description": tool.Description, "parameters": tool.Parameters,
		})
	}
	return tools
}

func openAISessionObject(options provider.RealtimeOptions) map[string]any {
	tools := openAITools(options.Tools)

	var maxTokens any = "inf"
	if options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	var transcription any
	if options.Input.Transcription != nil {
		model := options.Input.Transcription.Model
		if model == "" {
			model = "gpt-live-transcribe"
		}
		config := map[string]any{"model": model}
		if options.Input.Transcription.Language != "" {
			config["language"] = options.Input.Transcription.Language
		}
		if len(options.Input.Transcription.Languages) > 0 {
			config["languages"] = options.Input.Transcription.Languages
		}
		if options.Input.Transcription.Prompt != "" {
			config["prompt"] = options.Input.Transcription.Prompt
		}
		if options.Input.Transcription.Delay != "" {
			config["delay"] = options.Input.Transcription.Delay
		}
		transcription = config
	}
	var noiseReduction any
	if options.Input.NoiseReduction != "" {
		noiseReduction = map[string]any{"type": string(options.Input.NoiseReduction)}
	}

	modalities := make([]string, len(options.Output.Modalities))
	for i, modality := range options.Output.Modalities {
		modalities[i] = string(modality)
	}

	input := map[string]any{
		"format":          openAIAudioFormat(options.Input.Audio),
		"transcription":   transcription,
		"noise_reduction": noiseReduction,
	}
	// Leaving turn_detection out keeps the service's current setting.
	if options.Input.TurnDetection != nil {
		input["turn_detection"] = openAITurnDetection(options.Input.TurnDetection)
	}

	result := map[string]any{
		"type":              "realtime",
		"tracing":           nil,
		"output_modalities": modalities,
		"instructions":      options.Instructions,
		"max_output_tokens": maxTokens,
		"tools":             tools,
		"tool_choice":       openAIToolChoice(options.ToolChoice),
		"audio": map[string]any{
			"input": input,
			"output": map[string]any{
				"format": openAIAudioFormat(options.Output.Audio),
				"voice":  openAIVoice(options.Output.Voice),
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

func openAITurnDetection(detection *provider.RealtimeTurnDetection) any {
	if detection.Disabled() {
		return nil
	}

	detectionType := detection.Type
	if detectionType == "" {
		detectionType = provider.RealtimeTurnDetectionServer
	}
	config := map[string]any{
		"type":               string(detectionType),
		"create_response":    detection.CreateResponse,
		"interrupt_response": detection.InterruptResponse,
	}
	if detection.Eagerness != "" {
		config["eagerness"] = detection.Eagerness
	}
	if detection.Threshold != nil {
		config["threshold"] = *detection.Threshold
	}
	if detection.PrefixPadding > 0 {
		config["prefix_padding_ms"] = detection.PrefixPadding.Milliseconds()
	}
	if detection.SilenceDuration > 0 {
		config["silence_duration_ms"] = detection.SilenceDuration.Milliseconds()
	}
	if detection.IdleTimeout > 0 {
		config["idle_timeout_ms"] = detection.IdleTimeout.Milliseconds()
	}
	return config
}

func openAIVoice(voice string) any {
	if strings.HasPrefix(voice, "voice_") {
		return map[string]any{"id": voice}
	}
	return voice
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
	eventType := "input_audio_buffer.append"
	if s.translation {
		eventType = "session.input_audio_buffer.append"
	}
	return s.send(ctx, map[string]any{
		"type": eventType, "event_id": realtimeID("event"),
		"audio": base64.StdEncoding.EncodeToString(audio),
	})
}

func (s *openAIRealtimeSession) CommitAudio(ctx context.Context) error {
	eventType := "input_audio_buffer.commit"
	if s.translation {
		// A translation has no turns: committing flushes the stream and ends it.
		eventType = "session.close"
	}
	return s.send(ctx, map[string]any{"type": eventType, "event_id": realtimeID("event")})
}

func (s *openAIRealtimeSession) ClearAudio(ctx context.Context) error {
	return s.send(ctx, map[string]any{"type": "input_audio_buffer.clear", "event_id": realtimeID("event")})
}

func (s *openAIRealtimeSession) SendMessage(ctx context.Context, message provider.Message) error {
	if message.Text() == "" {
		return errors.New("openai realtime: text message is empty")
	}
	return s.send(ctx, map[string]any{
		"type": "conversation.item.create", "event_id": realtimeID("event"), "item": openAIItem(message),
	})
}

func openAIItem(message provider.Message) map[string]any {
	contentType := "input_text"
	if message.Role == provider.MessageRoleAssistant {
		contentType = "output_text"
	}
	item := map[string]any{
		"type": "message", "role": string(message.Role),
		"content": []map[string]any{{"type": contentType, "text": message.Text()}},
	}
	if message.Role == "" {
		item = map[string]any{"type": "item_reference"}
	}
	for _, content := range message.Content {
		if call := content.ToolCall; call != nil {
			item = map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Arguments}
		}
		if result := content.ToolResult; result != nil {
			var output strings.Builder
			for _, part := range result.Parts {
				output.WriteString(part.Text)
			}
			item = map[string]any{"type": "function_call_output", "call_id": result.ID, "output": output.String()}
		}
	}
	// Keeping the caller's item id lets it address the item later.
	if message.ID != "" {
		item["id"] = message.ID
	}
	return item
}

func (s *openAIRealtimeSession) DeleteItem(ctx context.Context, itemID string) error {
	if itemID == "" {
		return errors.New("openai realtime: conversation item id is required")
	}
	return s.send(ctx, map[string]any{
		"type": "conversation.item.delete", "event_id": realtimeID("event"), "item_id": itemID,
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
	if options == nil {
		return s.send(ctx, event)
	}
	response := map[string]any{}
	if options.Instructions != "" {
		response["instructions"] = options.Instructions
	}
	if len(options.OutputModalities) > 0 {
		modalities := make([]string, len(options.OutputModalities))
		for i, modality := range options.OutputModalities {
			modalities[i] = string(modality)
		}
		response["output_modalities"] = modalities
	}
	if options.MaxTokens != nil {
		response["max_output_tokens"] = *options.MaxTokens
	}
	if options.Tools != nil {
		response["tools"] = openAITools(options.Tools)
	}
	if tools := options.ToolOptions; tools != nil {
		if tools.Choice == provider.ToolChoiceAny && len(tools.Allowed) == 1 {
			response["tool_choice"] = map[string]any{"type": "function", "name": tools.Allowed[0]}
		} else if tools.Choice != "" {
			response["tool_choice"] = openAIToolChoice(tools.Choice)
		}
		if tools.DisableParallelToolCalls {
			response["parallel_tool_calls"] = false
		}
	}
	if reasoning := options.ReasoningOptions; reasoning != nil && reasoning.Effort != "" {
		response["reasoning"] = map[string]any{"effort": string(reasoning.Effort)}
	}
	if options.OutOfBand {
		response["conversation"] = "none"
	}
	if options.Input != nil {
		input := make([]map[string]any, len(options.Input))
		for i, message := range options.Input {
			input[i] = openAIItem(message)
		}
		response["input"] = input
	}
	if len(response) > 0 {
		event["response"] = response
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
	if err := ctx.Err(); err != nil {
		return err
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
			decodeErr := fmt.Errorf("openai realtime: decode event: %w", err)
			if !ready {
				s.signalReady(decodeErr)
				return
			}
			s.emit(provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: decodeErr})
			continue
		}
		if header.Type == "session.created" && !ready {
			ready = true
			s.signalReady(nil)
			continue
		}
		if header.Type == "error" && !ready {
			var value openAIRealtimeEvent
			_ = json.Unmarshal(data, &value)
			message := value.Error.Message
			if message == "" {
				message = "the provider rejected the realtime session"
			}
			s.signalReady(fmt.Errorf("openai realtime: session setup failed: %s", message))
			return
		}

		for _, event := range s.translate(data) {
			s.emit(event)
		}
		// OpenAI keeps the socket open after a translation ends.
		if header.Type == "session.closed" {
			return
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
	Type           string                    `json:"type"`
	EventID        string                    `json:"event_id"`
	ID             string                    `json:"id"`
	ItemID         string                    `json:"item_id"`
	PreviousItemID string                    `json:"previous_item_id"`
	ResponseID     string                    `json:"response_id"`
	ContentIndex   int                       `json:"content_index"`
	AudioStartMS   int64                     `json:"audio_start_ms"`
	AudioEndMS     int64                     `json:"audio_end_ms"`
	ElapsedMS      int64                     `json:"elapsed_ms"`
	Delta          string                    `json:"delta"`
	Intermediate   string                    `json:"intermediate"`
	Text           string                    `json:"text"`
	Transcript     string                    `json:"transcript"`
	Speaker        string                    `json:"speaker"`
	Start          float64                   `json:"start"`
	End            float64                   `json:"end"`
	Usage          *openAITranscriptionUsage `json:"usage"`
	Response       struct {
		ID             string         `json:"id"`
		ConversationID jsontext.Value `json:"conversation_id"`
		Status         string         `json:"status"`
		StatusDetails  struct {
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

type openAITranscriptionUsage struct {
	openAIRealtimeUsage
	Type    string  `json:"type"`
	Seconds float64 `json:"seconds"`
}

func (s *openAIRealtimeSession) translate(data []byte) []provider.RealtimeEvent {
	var value openAIRealtimeEvent
	if err := json.Unmarshal(data, &value); err != nil {
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventError, Err: fmt.Errorf("openai realtime: decode event: %w", err)}}
	}

	if value.ItemID == "" {
		switch value.Type {
		case "input_audio_buffer.committed":
			value.ItemID = oldest(s.uncommitted)
		case "conversation.item.input_audio_transcription.completed", "conversation.item.input_audio_transcription.failed":
			value.ItemID = oldest(s.incomplete)
		}
	}

	contentID := fmt.Sprintf("%s:%d", value.ItemID, value.ContentIndex)
	switch value.Type {
	case "response.created":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventResponseStarted, ResponseID: value.Response.ID, OutOfBand: value.Response.ConversationID.Kind() == 'n'}}

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
		contentType := s.contents[contentID]
		delete(s.contents, contentID)
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventContentDone, ResponseID: value.ResponseID, ContentID: contentID, ItemID: value.ItemID, ContentType: contentType}}

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
		s.trackTranscript(value.ItemID)
		var events []provider.RealtimeEvent
		text, ok := s.transcripts[contentID]
		if !ok {
			text = new(strings.Builder)
			s.transcripts[contentID] = text
			events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: contentID, ItemID: value.ItemID, ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser, Stage: provider.RealtimeGenerationFinal})
		}
		text.WriteString(value.Delta)
		events = append(events, provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: contentID, ItemID: value.ItemID, Text: value.Delta})
		return events

	case "conversation.item.input_audio_transcription.intermediate":
		// MAI's provisional suffix replaces the previous one and follows the
		// finalized deltas; together they form the current hypothesis.
		s.trackTranscript(value.ItemID)
		var final string
		if text := s.transcripts[contentID]; text != nil {
			final = text.String()
		}
		return []provider.RealtimeEvent{{
			Type: provider.RealtimeEventInputTranscriptionPreview, ContentID: contentID, ItemID: value.ItemID,
			Text: final + value.Intermediate, Stage: provider.RealtimeGenerationSpeculative,
		}}

	case "conversation.item.input_audio_transcription.completed":
		s.finishTranscript(value.ItemID)
		var events []provider.RealtimeEvent
		if _, ok := s.transcripts[contentID]; !ok {
			events = append(events,
				provider.RealtimeEvent{Type: provider.RealtimeEventContentStarted, ContentID: contentID, ItemID: value.ItemID, ContentType: provider.RealtimeContentText, Role: provider.MessageRoleUser, Stage: provider.RealtimeGenerationFinal},
				provider.RealtimeEvent{Type: provider.RealtimeEventTextDelta, ContentID: contentID, ItemID: value.ItemID, Text: value.Transcript},
			)
		}
		delete(s.transcripts, contentID)
		// The final transcript drops the leading space of the first delta.
		events = append(events, provider.RealtimeEvent{
			Type: provider.RealtimeEventContentDone, ContentID: contentID, ItemID: value.ItemID,
			ContentType: provider.RealtimeContentText, Text: value.Transcript,
			TranscriptionUsage: convertOpenAITranscriptionUsage(value.Usage),
		})
		return events

	case "session.output_audio.delta":
		audio, err := base64.StdEncoding.DecodeString(value.Delta)
		if err != nil {
			return []provider.RealtimeEvent{{Type: provider.RealtimeEventError, Err: fmt.Errorf("openai realtime: decode audio: %w", err)}}
		}
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventAudioDelta, Audio: audio, AudioEnd: time.Duration(value.ElapsedMS) * time.Millisecond}}

	case "session.output_transcript.delta":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventTextDelta, Role: provider.MessageRoleAssistant, Text: value.Delta, AudioEnd: time.Duration(value.ElapsedMS) * time.Millisecond}}

	case "session.input_transcript.delta":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventTextDelta, Role: provider.MessageRoleUser, Text: value.Delta, AudioEnd: time.Duration(value.ElapsedMS) * time.Millisecond}}

	case "session.closed":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventResponseDone}}

	case "input_audio_buffer.speech_started":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputSpeechStarted, ItemID: value.ItemID, AudioStart: time.Duration(value.AudioStartMS) * time.Millisecond}}
	case "input_audio_buffer.speech_stopped":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputSpeechStopped, ItemID: value.ItemID, AudioEnd: time.Duration(value.AudioEndMS) * time.Millisecond}}
	case "input_audio_buffer.committed":
		s.uncommitted = slices.DeleteFunc(s.uncommitted, func(id string) bool { return id == value.ItemID })
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputCommitted, ItemID: value.ItemID, PreviousItemID: value.PreviousItemID}}
	case "input_audio_buffer.cleared":
		return []provider.RealtimeEvent{{Type: provider.RealtimeEventInputCleared}}
	case "input_audio_buffer.timeout_triggered":
		return []provider.RealtimeEvent{{
			Type: provider.RealtimeEventInputTimeoutTriggered, ItemID: value.ItemID,
			AudioStart: time.Duration(value.AudioStartMS) * time.Millisecond,
			AudioEnd:   time.Duration(value.AudioEndMS) * time.Millisecond,
		}}
	case "conversation.item.input_audio_transcription.segment":
		return []provider.RealtimeEvent{{
			Type:   provider.RealtimeEventInputTranscriptionSegment,
			ItemID: value.ItemID, ContentIndex: value.ContentIndex,
			Text: value.Text, SegmentID: value.ID, Speaker: value.Speaker,
			SegmentStart: value.Start, SegmentEnd: value.End,
		}}

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
		return events

	case "conversation.item.input_audio_transcription.failed":
		s.finishTranscript(value.ItemID)
		delete(s.transcripts, contentID)
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

// trackTranscript records the first output of an input transcript item so
// that events without an item id can be attributed to it.
func (s *openAIRealtimeSession) trackTranscript(itemID string) {
	if itemID == "" || slices.Contains(s.incomplete, itemID) {
		return
	}
	s.uncommitted = append(s.uncommitted, itemID)
	s.incomplete = append(s.incomplete, itemID)
}

func (s *openAIRealtimeSession) finishTranscript(itemID string) {
	matches := func(id string) bool { return id == itemID }
	s.uncommitted = slices.DeleteFunc(s.uncommitted, matches)
	s.incomplete = slices.DeleteFunc(s.incomplete, matches)
}

func oldest(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[0]
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

func convertOpenAITranscriptionUsage(value *openAITranscriptionUsage) *provider.RealtimeTranscriptionUsage {
	usage := &provider.RealtimeTranscriptionUsage{}
	if value != nil {
		switch value.Type {
		case "tokens":
			usage.Tokens = convertOpenAIRealtimeUsage(&value.openAIRealtimeUsage)
		case "duration":
			duration := time.Duration(value.Seconds * float64(time.Second))
			usage.AudioDuration = &duration
		}
	}
	return usage
}

func realtimeID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
